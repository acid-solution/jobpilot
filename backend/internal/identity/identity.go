package identity

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const contextKey = "authenticated-user-id"

var (
	ErrInvalid     = errors.New("invalid identity")
	ErrUnavailable = errors.New("identity resolver is unavailable")
)

type Resolver interface {
	Resolve(*http.Request) (uuid.UUID, error)
}

type DevResolver struct {
	DefaultUserID uuid.UUID
}

func (r DevResolver) Resolve(request *http.Request) (uuid.UUID, error) {
	value := request.Header.Get("X-Dev-User-ID")
	if value == "" {
		return r.DefaultUserID, nil
	}
	return uuid.Parse(value)
}

type UnavailableResolver struct{}

func (UnavailableResolver) Resolve(*http.Request) (uuid.UUID, error) {
	return uuid.Nil, ErrUnavailable
}

func Middleware(resolver Resolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := resolver.Resolve(c.Request)
		if err != nil {
			status := http.StatusUnauthorized
			code := "invalid_identity"
			message := "无法识别当前用户"
			if errors.Is(err, ErrUnavailable) {
				status = http.StatusServiceUnavailable
				code = "identity_service_unavailable"
				message = "认证服务尚未配置"
			}
			c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
			return
		}
		c.Set(contextKey, userID)
		c.Next()
	}
}

func UserID(c *gin.Context) uuid.UUID {
	value, ok := c.Get(contextKey)
	if !ok {
		return uuid.Nil
	}
	userID, _ := value.(uuid.UUID)
	return userID
}
