package lbi

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseRelease(t *testing.T) {
	t.Parallel()

	data := []byte(`# LBI metadata
LBI_NAME="LTS Base Image"
LBI_SHORT='LBI'
LBI_VERSION=1.0
LBI_BUILD="001"
BASE_OS="Ubuntu 24.04.4 LTS"
MAINTAINER="Likone Technologies"
UNKNOWN_KEY=preserved-by-parser
`)

	values, warnings := parseRelease(data)
	if len(warnings) != 0 {
		t.Fatalf("parseRelease() warnings = %v, want none", warnings)
	}

	want := map[string]string{
		"LBI_NAME":    "LTS Base Image",
		"LBI_SHORT":   "LBI",
		"LBI_VERSION": "1.0",
		"LBI_BUILD":   "001",
		"BASE_OS":     "Ubuntu 24.04.4 LTS",
		"MAINTAINER":  "Likone Technologies",
		"UNKNOWN_KEY": "preserved-by-parser",
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("parseRelease() values = %#v, want %#v", values, want)
	}
}

func TestParseReleaseReportsMalformedLinesAndKeepsPartialData(t *testing.T) {
	t.Parallel()

	data := []byte("LBI_NAME=Valid\nnot-an-assignment\nLBI_SHORT=\"unterminated\n=missing-key\n")
	values, warnings := parseRelease(data)

	if values["LBI_NAME"] != "Valid" {
		t.Fatalf("LBI_NAME = %q, want Valid", values["LBI_NAME"])
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v, want 3 warnings", warnings)
	}
}

func TestCollectorReadsPartialMetadataAndIgnoresUnknownKeys(t *testing.T) {
	t.Parallel()

	collector := NewCollectorWithDependencies("/test/lbi-release", func(path string) ([]byte, error) {
		if path != "/test/lbi-release" {
			t.Fatalf("path = %q, want /test/lbi-release", path)
		}
		return []byte("LBI_NAME=\"LTS Base Image\"\nUNKNOWN=value\n"), nil
	})

	metadata, warnings := collector.Collect()
	if !metadata.Available || metadata.Name != "LTS Base Image" {
		t.Fatalf("metadata = %#v, want available partial metadata", metadata)
	}
	if metadata.Version != "" {
		t.Fatalf("Version = %q, want empty", metadata.Version)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
}

func TestCollectorMarksMissingFileUnavailable(t *testing.T) {
	t.Parallel()

	collector := NewCollectorWithDependencies(DefaultReleasePath, func(string) ([]byte, error) {
		return nil, errors.New("file does not exist")
	})

	metadata, warnings := collector.Collect()
	if metadata.Available {
		t.Fatal("Available = true, want false")
	}
	if len(warnings) != 1 || warnings[0].Source != "lbi" || !strings.Contains(warnings[0].Message, "file does not exist") {
		t.Fatalf("warnings = %#v, want one lbi warning", warnings)
	}
}

func TestCollectorReportsMalformedMetadata(t *testing.T) {
	t.Parallel()

	collector := NewCollectorWithDependencies(DefaultReleasePath, func(string) ([]byte, error) {
		return []byte("LBI_NAME='unterminated\nLBI_BUILD=001\n"), nil
	})

	metadata, warnings := collector.Collect()
	if !metadata.Available || metadata.Build != "001" {
		t.Fatalf("metadata = %#v, want available metadata with build", metadata)
	}
	if len(warnings) != 1 || warnings[0].Source != "lbi" {
		t.Fatalf("warnings = %#v, want one lbi warning", warnings)
	}
}
