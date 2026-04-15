package core

import (
	"testing"
)

func TestParseManifest_DirectURL(t *testing.T) {
	raw := []byte(`{"mimeType":"audio/flac","codecs":"flac","urls":["https://cdn.tidal.com/track.flac"]}`)
	result, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Segmented {
		t.Error("expected Segmented=false for single URL")
	}
	if len(result.URLs) != 1 {
		t.Errorf("expected 1 URL, got %d", len(result.URLs))
	}
	if result.MimeType != "audio/flac" {
		t.Errorf("expected MimeType=audio/flac, got %q", result.MimeType)
	}
}

func TestParseManifest_BTSMultipleSegments(t *testing.T) {
	raw := []byte(`{"mimeType":"audio/flac","codecs":"flac","urls":["https://cdn.tidal.com/seg1.flac","https://cdn.tidal.com/seg2.flac","https://cdn.tidal.com/seg3.flac"]}`)
	result, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Segmented {
		t.Error("expected Segmented=true for multiple URLs")
	}
	if len(result.URLs) != 3 {
		t.Errorf("expected 3 URLs, got %d", len(result.URLs))
	}
}

func TestParseManifest_DASHXML(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011">
  <Period>
    <AdaptationSet mimeType="audio/flac" codecs="flac">
      <Representation>
        <SegmentList>
          <SegmentURL media="https://cdn.tidal.com/seg001.flac"/>
          <SegmentURL media="https://cdn.tidal.com/seg002.flac"/>
          <SegmentURL media="https://cdn.tidal.com/seg003.flac"/>
        </SegmentList>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`)
	result, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Segmented {
		t.Error("expected Segmented=true for DASH with 3 segments")
	}
	if len(result.URLs) != 3 {
		t.Errorf("expected 3 URLs, got %d", len(result.URLs))
	}
	if result.URLs[0] != "https://cdn.tidal.com/seg001.flac" {
		t.Errorf("unexpected URLs[0]: %q", result.URLs[0])
	}
	if result.MimeType != "audio/flac" {
		t.Errorf("expected MimeType=audio/flac, got %q", result.MimeType)
	}
}

func TestParseManifest_EmptyInput(t *testing.T) {
	_, err := ParseManifest([]byte{})
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestParseManifest_InvalidJSON(t *testing.T) {
	_, err := ParseManifest([]byte("not json or xml"))
	if err == nil {
		t.Error("expected error for unrecognized format")
	}
}
