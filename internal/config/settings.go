package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
)

// Settings is the decoded user-global settings file, validated against #Config.
type Settings struct {
	ProjectRules   string
	GlobalRules    string
	FollowSymlinks *bool
	FailClosed     *bool
	Explain        string
	Format         string
	Color          string
	Log            string
	LogTTL         string
}

type settingsFile struct {
	ProjectRules   string `json:"project_rules"`
	GlobalRules    string `json:"global_rules"`
	FollowSymlinks *bool  `json:"follow_symlinks"`
	FailClosed     *bool  `json:"fail_closed"`
	Explain        string `json:"explain"`
	Format         string `json:"format"`
	Color          string `json:"color"`
	Log            any    `json:"log"`
	LogTTL         string `json:"log_ttl"`
}

// LoadSettings reads the settings file at path. A missing file yields zero Settings.
func LoadSettings(path string) (Settings, error) {
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read settings %s: %w", path, err)
	}
	bundle, err := loadSchema()
	if err != nil {
		return Settings{}, err
	}
	v := bundle.configDef.Unify(bundle.ctx.CompileBytes(src, cue.Filename(path)))
	if err := v.Validate(cue.Concrete(true)); err != nil {
		return Settings{}, fmt.Errorf("invalid settings %s: %w", path, err)
	}
	var file settingsFile
	if err := v.Decode(&file); err != nil {
		return Settings{}, fmt.Errorf("decode settings %s: %w", path, err)
	}
	settings := Settings{
		ProjectRules:   expandHome(file.ProjectRules),
		GlobalRules:    expandHome(file.GlobalRules),
		FollowSymlinks: file.FollowSymlinks,
		FailClosed:     file.FailClosed,
		Explain:        file.Explain,
		Format:         file.Format,
		Color:          file.Color,
		LogTTL:         file.LogTTL,
	}
	switch log := file.Log.(type) {
	case bool:
		if log {
			settings.Log = "1"
		}
	case string:
		settings.Log = expandHome(log)
	}
	return settings, nil
}

func expandHome(path string) string {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, rest)
}
