package core

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ExtensionManifest defines an extension's capabilities.
type ExtensionManifest struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Version       string        `json:"version"`
	MinAppVersion string        `json:"minAppVersion"`
	Author        string        `json:"author"`
	Description   string        `json:"description,omitempty"`
	Capabilities  []string      `json:"capabilities"` // "source", "metadata", "resolver"
	Permissions   []string      `json:"permissions"`  // "network"
	SourceConfig  *SourceExtCfg `json:"sourceConfig,omitempty"`
	AuthFields    []AuthField   `json:"authFields,omitempty"`
}

// SourceExtCfg describes how an extension source fetches data via HTTP.
type SourceExtCfg struct {
	Name             string            `json:"name"`
	DisplayName      string            `json:"displayName"`
	URLPattern       string            `json:"urlPattern"` // regex
	BaseURL          string            `json:"baseUrl"`
	TrackEndpoint    string            `json:"trackEndpoint,omitempty"`
	AlbumEndpoint    string            `json:"albumEndpoint,omitempty"`
	PlaylistEndpoint string            `json:"playlistEndpoint,omitempty"`
	SearchEndpoint   string            `json:"searchEndpoint,omitempty"`
	StreamEndpoint   string            `json:"streamEndpoint,omitempty"`
	TrackMapping     map[string]string `json:"trackMapping,omitempty"`
	AlbumMapping     map[string]string `json:"albumMapping,omitempty"`
	StreamURLField   string            `json:"streamUrlField,omitempty"`
}

// AuthField describes a credential field the user must provide.
type AuthField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"` // "text", "password"
	Required bool   `json:"required"`
}

// Extension represents an installed extension.
type Extension struct {
	Manifest ExtensionManifest `json:"manifest"`
	Enabled  bool              `json:"enabled"`
	Dir      string            `json:"dir"`
	AuthData map[string]string `json:"authData"`
	urlRegex *regexp.Regexp
}

// ExtensionManager handles extension lifecycle.
type ExtensionManager struct {
	extensions map[string]*Extension
	dataDir    string
	mu         sync.RWMutex
	httpClient *http.Client
	logger     *LogBuffer
}

// NewExtensionManager creates a manager that persists extensions under dataDir/extensions.
func NewExtensionManager(dataDir string, logger *LogBuffer) *ExtensionManager {
	em := &ExtensionManager{
		extensions: make(map[string]*Extension),
		dataDir:    filepath.Join(dataDir, "extensions"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
		logger:     logger,
	}
	if err := os.MkdirAll(em.dataDir, 0755); err != nil {
		logger.Warn("Could not create extensions dir: " + err.Error())
	}
	em.loadInstalled()
	return em
}

// loadInstalled scans the extensions directory for installed extensions.
func (em *ExtensionManager) loadInstalled() {
	entries, err := os.ReadDir(em.dataDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(em.dataDir, entry.Name(), "extension.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var manifest ExtensionManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			em.logger.Warn("Invalid extension manifest in " + entry.Name() + ": " + err.Error())
			continue
		}
		ext := &Extension{
			Manifest: manifest,
			Enabled:  true,
			Dir:      filepath.Join(em.dataDir, entry.Name()),
			AuthData: make(map[string]string),
		}
		if manifest.SourceConfig != nil && manifest.SourceConfig.URLPattern != "" {
			ext.urlRegex, _ = regexp.Compile(manifest.SourceConfig.URLPattern)
		}
		// Load persisted auth data
		authPath := filepath.Join(ext.Dir, "auth.json")
		if authData, err := os.ReadFile(authPath); err == nil {
			json.Unmarshal(authData, &ext.AuthData)
		}
		em.extensions[manifest.ID] = ext
	}
	if len(em.extensions) > 0 {
		em.logger.Info(fmt.Sprintf("Loaded %d extension(s)", len(em.extensions)))
	}
}

// Install downloads a ZIP from zipURL, extracts it, and registers the extension.
func (em *ExtensionManager) Install(zipURL string) (*ExtensionManifest, error) {
	em.mu.Lock()
	defer em.mu.Unlock()

	resp, err := em.httpClient.Get(zipURL)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "flacidal-ext-*.zip")
	if err != nil {
		return nil, fmt.Errorf("temp file creation failed: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return nil, fmt.Errorf("download write failed: %w", err)
	}
	tmpFile.Close()

	return em.installFromZip(tmpFile.Name())
}

func (em *ExtensionManager) installFromZip(zipPath string) (*ExtensionManifest, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("invalid zip: %w", err)
	}
	defer r.Close()

	// Find and parse manifest
	var manifest ExtensionManifest
	var found bool
	for _, f := range r.File {
		if filepath.Base(f.Name) == "extension.json" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("cannot open manifest: %w", err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("cannot read manifest: %w", err)
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return nil, fmt.Errorf("invalid manifest: %w", err)
			}
			found = true
			break
		}
	}
	if !found || manifest.ID == "" {
		return nil, fmt.Errorf("no valid extension.json found in zip")
	}

	// Extract to extensions dir (flatten)
	extDir := filepath.Join(em.dataDir, manifest.ID)
	os.RemoveAll(extDir)
	if err := os.MkdirAll(extDir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create extension dir: %w", err)
	}

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.Base(f.Name)
		outPath := filepath.Join(extDir, name)

		rc, err := f.Open()
		if err != nil {
			continue
		}
		outFile, err := os.Create(outPath)
		if err != nil {
			rc.Close()
			continue
		}
		io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
	}

	ext := &Extension{
		Manifest: manifest,
		Enabled:  true,
		Dir:      extDir,
		AuthData: make(map[string]string),
	}
	if manifest.SourceConfig != nil && manifest.SourceConfig.URLPattern != "" {
		ext.urlRegex, _ = regexp.Compile(manifest.SourceConfig.URLPattern)
	}
	em.extensions[manifest.ID] = ext
	em.logger.Success("Installed extension: " + manifest.Name + " v" + manifest.Version)

	return &manifest, nil
}

// Uninstall removes an extension by ID.
func (em *ExtensionManager) Uninstall(id string) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	ext, ok := em.extensions[id]
	if !ok {
		return fmt.Errorf("extension %s not found", id)
	}
	os.RemoveAll(ext.Dir)
	delete(em.extensions, id)
	em.logger.Info("Uninstalled extension: " + id)
	return nil
}

// SetEnabled enables or disables an extension.
func (em *ExtensionManager) SetEnabled(id string, enabled bool) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	ext, ok := em.extensions[id]
	if !ok {
		return fmt.Errorf("extension %s not found", id)
	}
	ext.Enabled = enabled
	return nil
}

// SetAuthData stores credentials for an extension (persisted to auth.json).
func (em *ExtensionManager) SetAuthData(id string, data map[string]string) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	ext, ok := em.extensions[id]
	if !ok {
		return fmt.Errorf("extension %s not found", id)
	}
	ext.AuthData = data

	authJSON, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialize auth data: %w", err)
	}
	return os.WriteFile(filepath.Join(ext.Dir, "auth.json"), authJSON, 0600)
}

// GetInstalled returns all installed extensions.
func (em *ExtensionManager) GetInstalled() []Extension {
	em.mu.RLock()
	defer em.mu.RUnlock()

	result := make([]Extension, 0, len(em.extensions))
	for _, ext := range em.extensions {
		result = append(result, *ext)
	}
	return result
}

// GetExtension returns a specific extension by ID.
func (em *ExtensionManager) GetExtension(id string) (*Extension, bool) {
	em.mu.RLock()
	defer em.mu.RUnlock()
	ext, ok := em.extensions[id]
	return ext, ok
}

// CanHandleURL checks if any enabled extension can handle a URL.
func (em *ExtensionManager) CanHandleURL(url string) (*Extension, bool) {
	em.mu.RLock()
	defer em.mu.RUnlock()

	for _, ext := range em.extensions {
		if ext.Enabled && ext.urlRegex != nil && ext.urlRegex.MatchString(url) {
			return ext, true
		}
	}
	return nil, false
}

// FetchFromExtension makes an HTTP request using the extension's API config.
func (em *ExtensionManager) FetchFromExtension(ext *Extension, endpointTemplate string, vars map[string]string) (map[string]interface{}, error) {
	if ext.Manifest.SourceConfig == nil {
		return nil, fmt.Errorf("extension has no source config")
	}

	url := ext.Manifest.SourceConfig.BaseURL + endpointTemplate
	for k, v := range vars {
		url = strings.ReplaceAll(url, "{"+k+"}", v)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot create request: %w", err)
	}
	for k, v := range ext.AuthData {
		req.Header.Set(k, v)
	}

	resp, err := em.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from extension API", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("cannot decode response: %w", err)
	}
	return result, nil
}

// ResolveJSONPath extracts a value from a nested map using dot notation (e.g. "data.title").
func ResolveJSONPath(data map[string]interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	var current interface{} = data
	for _, part := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current = m[part]
	}
	return current
}
