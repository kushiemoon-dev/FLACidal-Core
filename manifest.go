package core

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// ManifestResult holds the parsed result of an audio stream manifest.
type ManifestResult struct {
	URLs      []string
	MimeType  string
	Codecs    string
	Segmented bool
}

// jsonManifest maps the JSON manifest format returned by Tidal proxies.
type jsonManifest struct {
	MimeType       string   `json:"mimeType"`
	Codecs         string   `json:"codecs"`
	EncryptionType string   `json:"encryptionType"`
	URLs           []string `json:"urls"`
}

// DASH XML structs — map to MPEG-DASH MPD format.
type dashMPD struct {
	XMLName xml.Name    `xml:"urn:mpeg:dash:schema:mpd:2011 MPD"`
	Periods []dashPeriod `xml:"Period"`
}

type dashPeriod struct {
	AdaptationSets []dashAdaptationSet `xml:"AdaptationSet"`
}

type dashAdaptationSet struct {
	MimeType        string               `xml:"mimeType,attr"`
	Codecs          string               `xml:"codecs,attr"`
	Representations []dashRepresentation `xml:"Representation"`
}

type dashRepresentation struct {
	MimeType    string           `xml:"mimeType,attr"`
	Codecs      string           `xml:"codecs,attr"`
	SegmentList dashSegmentList  `xml:"SegmentList"`
	BaseURL     string           `xml:"BaseURL"`
}

type dashSegmentList struct {
	SegmentURLs []dashSegmentURL `xml:"SegmentURL"`
}

type dashSegmentURL struct {
	Media string `xml:"media,attr"`
}

// ParseManifest detects and parses a manifest from raw bytes.
// It supports DASH XML (starts with '<') and JSON (starts with '{').
func ParseManifest(raw []byte) (*ManifestResult, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty manifest")
	}
	switch trimmed[0] {
	case '<':
		return parseDASHManifest(trimmed)
	case '{':
		return parseJSONManifest(trimmed)
	default:
		return nil, fmt.Errorf("unrecognized manifest format")
	}
}

func parseDASHManifest(raw []byte) (*ManifestResult, error) {
	var mpd dashMPD
	if err := xml.Unmarshal(raw, &mpd); err != nil {
		return nil, fmt.Errorf("DASH XML parse error: %w", err)
	}

	var urls []string
	var mimeType, codecs string

	for _, period := range mpd.Periods {
		for _, as := range period.AdaptationSets {
			if mimeType == "" && as.MimeType != "" {
				mimeType = as.MimeType
			}
			if codecs == "" && as.Codecs != "" {
				codecs = as.Codecs
			}
			for _, rep := range as.Representations {
				if mimeType == "" && rep.MimeType != "" {
					mimeType = rep.MimeType
				}
				if codecs == "" && rep.Codecs != "" {
					codecs = rep.Codecs
				}
				for _, seg := range rep.SegmentList.SegmentURLs {
					if seg.Media != "" {
						urls = append(urls, seg.Media)
					}
				}
			}
		}
	}

	// Fall back to BaseURL if no SegmentURLs found.
	if len(urls) == 0 {
		for _, period := range mpd.Periods {
			for _, as := range period.AdaptationSets {
				for _, rep := range as.Representations {
					if rep.BaseURL != "" {
						urls = append(urls, rep.BaseURL)
					}
				}
			}
		}
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("DASH manifest: no segment URLs found")
	}

	return &ManifestResult{
		URLs:      urls,
		MimeType:  mimeType,
		Codecs:    codecs,
		Segmented: len(urls) > 1,
	}, nil
}

func parseJSONManifest(raw []byte) (*ManifestResult, error) {
	var m jsonManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("JSON manifest parse error: %w", err)
	}
	if len(m.URLs) == 0 {
		return nil, fmt.Errorf("JSON manifest: no URLs found")
	}
	return &ManifestResult{
		URLs:      m.URLs,
		MimeType:  m.MimeType,
		Codecs:    m.Codecs,
		Segmented: len(m.URLs) > 1,
	}, nil
}

// DownloadSegmented downloads N segments concurrently (up to 4 workers),
// concatenates them in order, and writes the result to output.
func DownloadSegmented(ctx context.Context, segments []string, output string, client *http.Client, onProgress func(done, total int)) error {
	total := len(segments)
	workers := total
	if workers > 4 {
		workers = 4
	}

	tmpDir, err := os.MkdirTemp(filepath.Dir(output), "flac-segments-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFiles := make([]string, total)
	for i := range tmpFiles {
		tmpFiles[i] = filepath.Join(tmpDir, fmt.Sprintf("seg%05d", i))
	}

	// Bounded concurrency via semaphore channel.
	sem := make(chan struct{}, workers)
	errs := make([]error, total)
	var wg sync.WaitGroup

	for i, url := range segments {
		wg.Add(1)
		go func(idx int, u string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := downloadSegment(ctx, client, u, tmpFiles[idx]); err != nil {
				errs[idx] = err
				return
			}
			if onProgress != nil {
				// Progress events may arrive out-of-order due to concurrent workers.
				// Callers must not assume monotonically increasing segment indices.
				onProgress(idx+1, total)
			}
		}(i, url)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			return fmt.Errorf("segment %d failed: %w", i, e)
		}
	}

	// Concatenate segments in order into the output file.
	out, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer out.Close()

	for _, tmp := range tmpFiles {
		f, err := os.Open(tmp)
		if err != nil {
			return fmt.Errorf("open segment %s: %w", tmp, err)
		}
		if _, err := io.Copy(out, f); err != nil {
			f.Close()
			return fmt.Errorf("concat segment %s: %w", tmp, err)
		}
		f.Close()
	}

	return nil
}

// downloadSegment fetches a single segment URL and writes it to outputPath.
func downloadSegment(ctx context.Context, client *http.Client, url, outputPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "FLACidal/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: unexpected status %d", url, resp.StatusCode)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", outputPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}

	return nil
}
