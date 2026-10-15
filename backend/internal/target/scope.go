package target

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"sort"
)

func GoalSignature(t Target) string {
	type direction struct {
		Category  uuid.UUID
		Specialty *uuid.UUID
	}
	values := make([]direction, 0, len(t.Directions))
	for _, d := range t.Directions {
		values = append(values, direction{d.CategoryID, d.SpecialtyID})
	}
	sort.Slice(values, func(i, j int) bool {
		a, b := values[i], values[j]
		if a.Category != b.Category {
			return a.Category.String() < b.Category.String()
		}
		if a.Specialty == nil && b.Specialty == nil {
			return false
		}
		if a.Specialty == nil {
			return true
		}
		if b.Specialty == nil {
			return false
		}
		return a.Specialty.String() < b.Specialty.String()
	})
	raw, _ := json.Marshal(struct {
		Type       string
		Year       *int
		Directions []direction
	}{t.EmploymentType, t.GraduationYear, values})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
