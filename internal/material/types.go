// Package material owns learning material, source files, normalized content, and briefs.
package material

import (
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("material not found")
	ErrInvalid      = errors.New("invalid material")
	ErrNameTaken    = errors.New("material name taken")
	ErrInvalidState = errors.New("material state does not allow action")
	ErrFileNotFound = errors.New("material file not found")
	ErrFileSelected = errors.New("material is selected by an active session")
)

type Limits struct {
	MaxFileBytes, MaxMaterialBytes int64
	MaxPages, MaxFiles             int
	MaxImageMegapixels             int
}

type Material struct {
	ID, CourseID, Scope, Name, Kind, Format, State     string
	OwnerUserID, ExternalURL, BriefSource, FailureCode string
	Brief                                              *Brief
	Approved                                           bool
	CreatedAt                                          time.Time
	BriefUpdatedAt, ApprovedAt, UpdatedAt              *time.Time
	BriefUpdatedBy, ApprovedBy                         string
}

type File struct {
	ID, MaterialID, OriginalFilename, MediaType, State, FailureCode string
	SizeBytes                                                       int64
	PageCount                                                       int
	CreatedAt                                                       time.Time
}

type CreateInput struct {
	CourseID, ActorID, Scope, Name, Kind, Format, ExternalURL string
	Brief                                                     *Brief
}

type ListInput struct{ Limit, Offset int }
type ListResult struct {
	Materials []Material
	HasMore   bool
}

type Brief struct {
	Version          int       `json:"version"`
	Summary          string    `json:"summary"`
	Subjects         []string  `json:"subjects"`
	LearningGoals    []string  `json:"learning_goals"`
	Sections         []Section `json:"sections"`
	Warnings         []string  `json:"warnings"`
	EducationalLevel *string   `json:"educational_level"`
}

type Section struct {
	Sequence    int      `json:"sequence"`
	Label       *string  `json:"label"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	SearchTerms []string `json:"search_terms"`
}

type Segment struct {
	Version      int     `json:"version"`
	Sequence     int     `json:"sequence"`
	ChapterLabel *string `json:"chapter_label"`
	SectionLabel *string `json:"section_label"`
	Text         string  `json:"text"`
}

type extractionResult struct {
	Content []byte
	Input   int64
}

type summaryResult struct {
	Brief  Brief
	Input  int64
	Output int64
}

func cloneBrief(value Brief) *Brief {
	cloned := value
	cloned.Subjects = cloneStrings(value.Subjects)
	cloned.LearningGoals = cloneStrings(value.LearningGoals)
	cloned.Warnings = cloneStrings(value.Warnings)
	if value.Sections != nil {
		cloned.Sections = make([]Section, len(value.Sections))
		copy(cloned.Sections, value.Sections)
	}
	if value.EducationalLevel != nil {
		level := *value.EducationalLevel
		cloned.EducationalLevel = &level
	}
	for index := range cloned.Sections {
		cloned.Sections[index].SearchTerms = cloneStrings(value.Sections[index].SearchTerms)
		if value.Sections[index].Label != nil {
			label := *value.Sections[index].Label
			cloned.Sections[index].Label = &label
		}
	}
	return &cloned
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
