package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const configFileName = "config.json"

// Config persists user-level SMem overrides under ~/.smem/config.json.
type Config struct {
	ProjectBindings map[string]string `json:"project_bindings,omitempty"`
}

// ConfigPath returns the path to SMem's global config file (~/.smem/config.json).
func ConfigPath(smemHome string) string {
	if strings.TrimSpace(smemHome) == "" {
		return ""
	}
	return filepath.Join(smemHome, configFileName)
}

// LoadConfig loads ~/.smem/config.json; returns an initialized empty Config if missing.
func LoadConfig(smemHome string) (Config, error) {
	cfg := Config{
		ProjectBindings: make(map[string]string),
	}
	path := ConfigPath(smemHome)
	if path == "" {
		return cfg, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("读取配置文件 %s 失败：%w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return cfg, nil
	}

	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置文件 %s 失败：%w", path, err)
	}
	if cfg.ProjectBindings == nil {
		cfg.ProjectBindings = make(map[string]string)
	}
	return cfg, nil
}

// SaveConfig persists SMem config cleanly under ~/.smem/config.json.
func SaveConfig(smemHome string, cfg Config) error {
	path := ConfigPath(smemHome)
	if path == "" {
		return errors.New("SMem 主目录为空，无法保存配置")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("创建配置目录失败：%w", err)
	}
	if cfg.ProjectBindings == nil {
		cfg.ProjectBindings = make(map[string]string)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败：%w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.tmp.*")
	if err != nil {
		return os.WriteFile(path, data, 0600)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		// Fallback on Windows if target lock prevents direct atomic rename.
		return os.WriteFile(path, data, 0600)
	}
	return nil
}

// BindProject maps a repository ID to a custom project memory directory in ~/.smem/config.json.
func BindProject(smemHome, repoID, targetDir string) (string, error) {
	if strings.TrimSpace(smemHome) == "" {
		return "", errors.New("SMem 主目录为空")
	}
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return "", errors.New("当前目录不在 Git 仓库中，无法确定项目 ID")
	}
	targetDir = strings.TrimSpace(targetDir)
	if targetDir == "" {
		return "", errors.New("目标记忆目录不能为空")
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return "", fmt.Errorf("解析目录路径失败：%w", err)
	}
	absDir = filepath.Clean(absDir)

	cfg, err := LoadConfig(smemHome)
	if err != nil {
		return "", err
	}
	cfg.ProjectBindings[repoID] = absDir
	if err := SaveConfig(smemHome, cfg); err != nil {
		return "", err
	}
	return absDir, nil
}

// UnbindProject removes a custom project memory directory mapping for repoID.
// Returns true if an existing binding was removed.
func UnbindProject(smemHome, repoID string) (bool, error) {
	if strings.TrimSpace(smemHome) == "" {
		return false, errors.New("SMem 主目录为空")
	}
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return false, errors.New("当前目录不在 Git 仓库中，无法确定项目 ID")
	}

	cfg, err := LoadConfig(smemHome)
	if err != nil {
		return false, err
	}
	if _, existed := cfg.ProjectBindings[repoID]; !existed {
		return false, nil
	}
	delete(cfg.ProjectBindings, repoID)
	if err := SaveConfig(smemHome, cfg); err != nil {
		return false, err
	}
	return true, nil
}

// LookupProjectBinding reads any custom memory directory bound to repoID.
func LookupProjectBinding(smemHome, repoID string) (string, bool, error) {
	if strings.TrimSpace(smemHome) == "" || strings.TrimSpace(repoID) == "" {
		return "", false, nil
	}
	cfg, err := LoadConfig(smemHome)
	if err != nil {
		return "", false, err
	}
	raw := strings.TrimSpace(cfg.ProjectBindings[repoID])
	if raw == "" {
		return "", false, nil
	}
	return filepath.Clean(raw), true, nil
}

// RefreshBinding reloads BoundDir from ~/.smem/config.json so long-lived MCP servers pick up changes dynamically.
func RefreshBinding(layout Layout) Layout {
	if !layout.InProject || strings.TrimSpace(layout.SmemHome) == "" || strings.TrimSpace(layout.RepoID) == "" {
		return layout
	}
	bound, ok, err := LookupProjectBinding(layout.SmemHome, layout.RepoID)
	if err != nil {
		return layout
	}
	if ok {
		layout.BoundDir = bound
	} else {
		layout.BoundDir = ""
	}
	return layout
}
