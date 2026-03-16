package factory

import (
	"fmt"
	"os"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// DalFactory represents a parsed .dalfactory CUE definition.
type DalFactory struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Template string   `json:"template"`
	Services []string `json:"services"`
}

// ParseFile reads and validates a .dalfactory CUE file.
func ParseFile(path string) (*DalFactory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read factory file: %w", err)
	}
	return Parse(data)
}

// Parse parses CUE bytes into a DalFactory.
func Parse(data []byte) (*DalFactory, error) {
	ctx := cuecontext.New()
	val := ctx.CompileBytes(data)
	if err := val.Err(); err != nil {
		return nil, fmt.Errorf("compile cue: %w", err)
	}
	if err := val.Validate(cue.Concrete(true)); err != nil {
		return nil, fmt.Errorf("validate cue: %w", err)
	}

	f := &DalFactory{}

	if v := val.LookupPath(cue.ParsePath("name")); v.Exists() {
		f.Name, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("version")); v.Exists() {
		f.Version, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("template")); v.Exists() {
		f.Template, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("services")); v.Exists() {
		iter, err := v.List()
		if err == nil {
			for iter.Next() {
				s, _ := iter.Value().String()
				f.Services = append(f.Services, s)
			}
		}
	}

	if f.Name == "" {
		return nil, fmt.Errorf("factory: 'name' field is required")
	}
	return f, nil
}
