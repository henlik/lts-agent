package system

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestNormalizeArchitecture(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"amd64":   "x86_64",
		"arm64":   "aarch64",
		"386":     "i386",
		"ppc64le": "ppc64le",
		"":        "",
	}
	for input, want := range tests {
		if got := NormalizeArchitecture(input); got != want {
			t.Errorf("NormalizeArchitecture(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCollectOSUsesPrettyNameAndFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "pretty name", data: "PRETTY_NAME=\"Ubuntu 24.04.4 LTS\"\n", want: "Ubuntu 24.04.4 LTS"},
		{name: "name and version", data: "NAME=Ubuntu\nVERSION='24.04.4 LTS'\n", want: "Ubuntu 24.04.4 LTS"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := collectOS(func(string) ([]byte, error) { return []byte(test.data), nil })
			if err != nil || got != test.want {
				t.Fatalf("collectOS() = %q, %v; want %q, nil", got, err, test.want)
			}
		})
	}
}

func TestCollectOSReportsUnavailableAndMalformedSources(t *testing.T) {
	t.Parallel()

	if _, err := collectOS(func(string) ([]byte, error) { return nil, errors.New("denied") }); err == nil {
		t.Fatal("collectOS() error = nil, want read error")
	}
	if _, err := collectOS(func(string) ([]byte, error) { return []byte("PRETTY_NAME=\"unterminated\n"), nil }); err == nil {
		t.Fatal("collectOS() error = nil, want parse error")
	}
}

func TestCollectKernelFallbacks(t *testing.T) {
	t.Parallel()

	kernel, err := collectKernel(
		context.Background(),
		func(string) ([]byte, error) { return []byte("6.8.0-test\n"), nil },
		func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("must not run") },
	)
	if err != nil || kernel != "6.8.0-test" {
		t.Fatalf("collectKernel(file) = %q, %v", kernel, err)
	}

	kernel, err = collectKernel(
		context.Background(),
		func(string) ([]byte, error) { return nil, errors.New("missing proc") },
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "uname" || len(args) != 1 || args[0] != "-r" {
				t.Fatalf("command = %s %v, want uname -r", name, args)
			}
			return []byte("6.8.0-fallback\n"), nil
		},
	)
	if err != nil || kernel != "6.8.0-fallback" {
		t.Fatalf("collectKernel(command) = %q, %v", kernel, err)
	}
}

func TestCollectKernelReportsAllFailures(t *testing.T) {
	t.Parallel()

	_, err := collectKernel(
		context.Background(),
		func(string) ([]byte, error) { return nil, errors.New("missing") },
		func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unavailable") },
	)
	if err == nil {
		t.Fatal("collectKernel() error = nil, want error")
	}
}

func TestCollectTimezoneFallbacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		readFile func(string) ([]byte, error)
		readlink func(string) (string, error)
		location func() *time.Location
		want     string
	}{
		{
			name:     "timezone file",
			readFile: func(string) ([]byte, error) { return []byte("Africa/Lubumbashi\n"), nil },
			readlink: func(string) (string, error) { return "", errors.New("unused") },
			location: func() *time.Location { return time.UTC },
			want:     "Africa/Lubumbashi",
		},
		{
			name:     "localtime symlink",
			readFile: func(string) ([]byte, error) { return nil, errors.New("missing") },
			readlink: func(string) (string, error) { return "/usr/share/zoneinfo/Africa/Lubumbashi", nil },
			location: func() *time.Location { return time.UTC },
			want:     "Africa/Lubumbashi",
		},
		{
			name:     "runtime location",
			readFile: func(string) ([]byte, error) { return nil, errors.New("missing") },
			readlink: func(string) (string, error) { return "", errors.New("missing") },
			location: func() *time.Location { return time.FixedZone("CAT", 2*60*60) },
			want:     "CAT",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := collectTimezone(test.readFile, test.readlink, test.location)
			if err != nil || got != test.want {
				t.Fatalf("collectTimezone() = %q, %v; want %q, nil", got, err, test.want)
			}
		})
	}
}

func TestCollectorReturnsPartialInventoryAndWarnings(t *testing.T) {
	t.Parallel()

	deps := Dependencies{
		ReadFile: func(path string) ([]byte, error) {
			switch path {
			case defaultOSReleasePath:
				return []byte("PRETTY_NAME=Test Linux\n"), nil
			default:
				return nil, fmt.Errorf("missing %s", path)
			}
		},
		Readlink: func(string) (string, error) { return "", errors.New("missing") },
		Hostname: func() (string, error) { return "", errors.New("hostname unavailable") },
		RunCommand: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("command unavailable")
		},
		Architecture: "amd64",
		Location:     func() *time.Location { return time.Local },
	}

	result, warnings := NewCollectorWithDependencies(deps).Collect(context.Background())
	if result.System.OS != "Test Linux" || result.System.Architecture != "x86_64" {
		t.Fatalf("result = %#v, want partial OS and architecture", result)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %#v, want hostname, kernel, and timezone warnings", warnings)
	}
}
