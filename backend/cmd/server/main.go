package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/config"
	"github.com/LeoninCS/jobpilot-next/backend/internal/embedding"
	"github.com/LeoninCS/jobpilot-next/backend/internal/httpapi"
	"github.com/LeoninCS/jobpilot-next/backend/internal/identity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/model/deepseek"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/LeoninCS/jobpilot-next/backend/internal/secure"
	"github.com/LeoninCS/jobpilot-next/backend/internal/storage/postgres"
	"github.com/LeoninCS/jobpilot-next/backend/internal/storage/postgres/migrations"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/pressly/goose/v3"
)

func main() {
	if err := run(); err != nil {
		slog.Error("jobpilot stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := config.Load()
	if err != nil {
		return err
	}

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStartup()
	database, err := postgres.Open(startupContext, configuration.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.Up(database, "sql"); err != nil {
		return err
	}
	lockStartupContext, cancelLockStartup := context.WithTimeout(context.Background(), 10*time.Second)
	mutationLocker, err := postgres.NewUserMutationLocker(lockStartupContext, configuration.DatabaseURL)
	cancelLockStartup()
	if err != nil {
		return err
	}
	defer mutationLocker.Close()

	var resolver identity.Resolver
	if configuration.AuthMode == "dev" {
		resolver = identity.DevResolver{DefaultUserID: configuration.DevUserID}
	} else {
		resolver = identity.NewJWKSResolver(
			configuration.AuthJWKSURL,
			configuration.AuthIssuer,
			configuration.AuthAudience,
			nil,
			5*time.Minute,
		)
	}

	targetRepository := postgres.NewTargetRepository(database)
	targetService := target.NewService(targetRepository)
	reviewQuota := postgres.AbilityReviewQuotaLimits{
		UserDaily:   configuration.AbilityReviewUserDailyLimit,
		GlobalDaily: configuration.AbilityReviewGlobalDailyLimit,
	}
	marketRepository := postgres.NewMarketRepository(database, reviewQuota)
	marketService := market.NewService(marketRepository, targetService)
	credentialCipher, err := secure.NewCipher(configuration.CredentialEncryptionKey)
	if err != nil {
		return err
	}
	deepSeekClient := deepseek.NewClient(configuration.DeepSeekBaseURL, nil)
	modelConfigRepository := postgres.NewModelConfigRepository(database)
	modelConfigService := modelconfig.NewService(modelConfigRepository, credentialCipher, deepSeekClient)
	embeddingRepository := postgres.NewEmbeddingRepository(database)
	embeddingClient := embedding.NewClient(configuration.EmbeddingBaseURL, configuration.PlatformEmbeddingAPIKey, nil)
	profileRepository := postgres.NewProfileRepository(database)
	profileAssessor := profile.NewModelAssessor(modelConfigService, deepSeekClient)
	if configuration.EmbeddingEnabled && embeddingClient.Configured() {
		profileAssessor.SetVector(embeddingClient, embeddingRepository)
	}
	profileService := profile.NewService(profileRepository, profileAssessor)
	knowledgeGapsService := knowledgegaps.NewService(postgres.NewKnowledgeGapsRepository(database), targetService, marketService, profileService)
	projectRepository := postgres.NewProjectRecommendationRepository(database)
	projectService := projectrecs.NewService(projectRepository, targetService, marketService, profileService, knowledgeGapsService, modelConfigService)
	projectWorker := projectrecs.NewWorker(projectRepository, projectService, modelConfigService, deepSeekClient,
		projectrecs.NewGitHubClient("https://api.github.com", configuration.GitHubToken, nil), 2*time.Second)
	analysisRepository := postgres.NewAnalysisRepository(database)
	analysisWorker := jdanalysis.NewWorker(analysisRepository, modelConfigService, deepSeekClient, 2*time.Second,
		jdanalysis.ClassificationReviewConfig{
			Enabled: configuration.JobClassificationReviewEnabled, Reviewer: deepSeekClient,
			APIKey: configuration.PlatformDeepSeekAPIKey, Model: configuration.PlatformReviewModel,
		})
	var vectorNormalizer *jdanalysis.VectorNormalizer
	if configuration.EmbeddingEnabled && embeddingClient.Configured() {
		vectorNormalizer = &jdanalysis.VectorNormalizer{Embedder: embeddingClient, Search: embeddingRepository, Model: deepSeekClient}
		analysisWorker.SetNormalizer(vectorNormalizer)
	}
	reviewRepository := postgres.NewAbilityReviewRepository(database, reviewQuota)
	reviewWorker := abilityreview.NewWorker(reviewRepository, deepSeekClient, configuration.AbilityReviewEnabled,
		configuration.PlatformDeepSeekAPIKey, configuration.PlatformReviewModel, 2*time.Second)
	gradingRepository := postgres.NewAbilityGradingRepository(database)
	gradingWorker := abilitygrading.NewWorker(gradingRepository, deepSeekClient, configuration.JDAbilityGradingEnabled,
		configuration.PlatformDeepSeekAPIKey, configuration.PlatformReviewModel, 2*time.Second)
	runtimeContext, cancelRuntime := context.WithCancel(context.Background())
	defer cancelRuntime()
	go analysisWorker.Run(runtimeContext)
	go reviewWorker.Run(runtimeContext)
	go gradingWorker.Run(runtimeContext)
	go projectWorker.Run(runtimeContext)
	if configuration.EmbeddingEnabled {
		go embedding.NewWorker(embeddingRepository, embeddingClient).Run(runtimeContext)
		if embeddingClient.Configured() {
			go profile.NewReassessmentWorker(postgres.NewMaterialReassessmentRepository(database, profileRepository, embeddingRepository), profileAssessor).Run(runtimeContext)
			go jdanalysis.NewNormalizationWorker(postgres.NewJDNormalizationRepository(database, analysisRepository, embeddingRepository), modelConfigService, vectorNormalizer).Run(runtimeContext)
		}
	}
	var agentService httpapi.AgentService
	if configuration.AgentEnabled {
		agentRepository := postgres.NewAgentRepository(database)
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				if err := agentRepository.RecoverRuns(runtimeContext); err != nil {
					slog.Error("recover Agent runs", "error", err)
				}
				select {
				case <-runtimeContext.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		agentService = &agent.Service{
			Repo: agentRepository, Checkpoints: postgres.NewAgentCheckpointStore(database),
			MutationLocker: mutationLocker,
			Transactions:   agentRepository,
			Targets:        targetService, Market: marketService, Profile: profileService,
			Gaps: knowledgeGapsService, Projects: projectService, Credentials: modelConfigService,
			Vectors:          embeddingRepository,
			Embedder:         embeddingClient,
			EmbeddingEnabled: configuration.EmbeddingEnabled,
			DeepSeekBaseURL:  configuration.DeepSeekBaseURL,
		}
	}

	router := httpapi.NewRouter(httpapi.Dependencies{
		AgentService:                  agentService,
		MutationLocker:                mutationLocker,
		IdentityResolver:              resolver,
		TargetService:                 targetService,
		MarketService:                 marketService,
		ModelConfigService:            modelConfigService,
		ProfileService:                profileService,
		KnowledgeGapsService:          knowledgeGapsService,
		ProjectRecommendationsService: projectService,
		Ready: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return database.PingContext(ctx)
		},
	})
	server := &http.Server{
		Addr:              configuration.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverError := make(chan error, 1)
	go func() {
		slog.Info("jobpilot api listening", "address", configuration.HTTPAddr)
		serverError <- server.ListenAndServe()
	}()

	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-shutdownSignal.Done():
		cancelRuntime()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	case err := <-serverError:
		cancelRuntime()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
