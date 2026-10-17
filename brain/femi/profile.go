package femi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Profile is FAMILY_DIR/profile.json (see family/profile.example.json).
type Profile struct {
	Family      string       `json:"family,omitempty"`
	Home        string       `json:"home,omitempty"`
	Notes       string       `json:"notes,omitempty"`
	Members     []Member     `json:"members,omitempty"`
	Medications []Medication `json:"medications,omitempty"`
}

// Member is one person in the family (or a close contact).
type Member struct {
	Name     string `json:"name"`
	Relation string `json:"relation,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// Medication is a daily medicine reminder.
type Medication struct {
	Who        string `json:"who"`
	Time       string `json:"time"` // "HH:MM", 24-hour, local
	What       string `json:"what"`
	EscalateTo string `json:"escalate_to,omitempty"`
}

// LoadProfile reads a profile; a missing file is not an error (nil profile).
func LoadProfile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

// PromptContext renders the profile for the system prompt.
func (p *Profile) PromptContext() string {
	if p == nil {
		return ""
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	return string(b)
}

// Label names a family member for messages, e.g. "Asha (grandmother)".
func (p *Profile) Label(name string) string {
	if p != nil {
		for _, m := range p.Members {
			if strings.EqualFold(m.Name, name) && m.Relation != "" {
				return fmt.Sprintf("%s (%s)", m.Name, m.Relation)
			}
		}
	}
	return name
}
