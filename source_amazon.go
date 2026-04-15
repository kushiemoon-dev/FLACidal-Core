package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

var defaultAmazonProxyEndpoints = []string{
	"https://doubledouble.top",
	"https://lucida.to",
}

// AmazonSource implements MusicSource as a download-only fallback via proxy pool.
type AmazonSource struct {
	pool *EndpointPool
}

// NewAmazonSource creates a new AmazonSource using the default proxy endpoints.
func NewAmazonSource() *AmazonSource {
	return &AmazonSource{
		pool: NewEndpointPool(defaultAmazonProxyEndpoints, 5*time.Minute),
	}
}

// Name returns the source identifier.
func (a *AmazonSource) Name() string {
	return "amazon"
}

// DisplayName returns the human-readable name.
func (a *AmazonSource) DisplayName() string {
	return "Amazon Music"
}

// IsAvailable checks if at least one proxy endpoint is healthy.
func (a *AmazonSource) IsAvailable() bool {
	return len(a.pool.GetHealthy()) > 0
}

// ParseURL always returns an error — Amazon source is download-only, not URL-routed.
func (a *AmazonSource) ParseURL(rawURL string) (id string, contentType string, err error) {
	return "", "", fmt.Errorf("amazon: URL parsing not supported")
}

// CanHandleURL checks if this source can handle the given URL.
func (a *AmazonSource) CanHandleURL(rawURL string) bool {
	_, _, err := a.ParseURL(rawURL)
	return err == nil
}

// GetTrack returns nil — Amazon source does not support track lookup by ID.
func (a *AmazonSource) GetTrack(id string) (*SourceTrack, error) {
	return nil, fmt.Errorf("amazon: not supported")
}

// GetAlbum returns nil — Amazon source does not support album lookup.
func (a *AmazonSource) GetAlbum(id string) (*SourceAlbum, error) {
	return nil, fmt.Errorf("amazon: not supported")
}

// GetPlaylist returns nil — Amazon source does not support playlist lookup.
func (a *AmazonSource) GetPlaylist(id string) (*SourcePlaylist, error) {
	return nil, fmt.Errorf("amazon: not supported")
}

// GetStreamURL fetches the stream URL for a track via the proxy pool.
func (a *AmazonSource) GetStreamURL(trackID string, quality string) (string, error) {
	ctx := context.Background()
	result, err := a.pool.RaceRequest(ctx, "/api/amazon?trackId="+url.QueryEscape(trackID))
	if err != nil {
		return "", fmt.Errorf("amazon: stream URL request failed: %w", err)
	}

	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(result.Body, &resp); err != nil {
		return "", fmt.Errorf("amazon: failed to parse stream URL response: %w", err)
	}
	if resp.URL == "" {
		return "", fmt.Errorf("amazon: empty stream URL returned")
	}
	return resp.URL, nil
}

// DownloadTrack downloads a track to outputDir using the proxy pool stream URL.
func (a *AmazonSource) DownloadTrack(trackID string, outputDir string, options DownloadOptions) (*DownloadResult, error) {
	streamURL, err := a.GetStreamURL(trackID, options.Quality)
	if err != nil {
		return nil, err
	}

	filename := buildFilename(options.FileNameFormat, "", trackID, "", 0)
	if filename == "" {
		filename = trackID
	}
	outputPath := filepath.Join(outputDir, filename+".flac")

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("amazon: failed to create output directory: %w", err)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	httpResp, err := client.Get(streamURL)
	if err != nil {
		return nil, fmt.Errorf("amazon: download request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("amazon: download failed with HTTP %d", httpResp.StatusCode)
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("amazon: failed to create output file: %w", err)
	}
	defer file.Close()

	size, err := io.Copy(file, httpResp.Body)
	if err != nil {
		file.Close()
		os.Remove(outputPath)
		return nil, fmt.Errorf("amazon: failed to write file: %w", err)
	}

	return &DownloadResult{
		Title:    trackID,
		FilePath: outputPath,
		FileSize: size,
		Source:   "amazon",
		Success:  true,
	}, nil
}

// SearchTrackByISRC searches for a track by ISRC via the proxy pool.
func (a *AmazonSource) SearchTrackByISRC(isrc string) (*SourceTrack, error) {
	if isrc == "" {
		return nil, fmt.Errorf("amazon: ISRC is empty")
	}

	ctx := context.Background()
	result, err := a.pool.RaceRequest(ctx, "/api/amazon/search?isrc="+url.QueryEscape(isrc))
	if err != nil {
		return nil, fmt.Errorf("amazon: ISRC search failed: %w", err)
	}

	var track SourceTrack
	if err := json.Unmarshal(result.Body, &track); err != nil {
		return nil, fmt.Errorf("amazon: failed to parse search response: %w", err)
	}
	if track.ID == "" {
		return nil, fmt.Errorf("amazon: no track found for ISRC %s", isrc)
	}
	track.Source = "amazon"
	return &track, nil
}
