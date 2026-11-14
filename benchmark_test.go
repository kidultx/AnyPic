package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The fixed benchmark set lives in testdata/benchmark; regenerate the images
// with `go run ./testdata/benchmark/generate`, and re-record this baseline
// after a deliberate encoder-parameter change with:
//
//	go test -run TestBenchmarkSet -update-benchmark .
//
// The test fails when a result grows by more than 2% or loses more than 0.002
// SSIM against the baseline, so an accidental quality/size regression is
// caught without pinning exact encoder bytes across Go/toolchain updates.
var updateBenchmark = flag.Bool("update-benchmark", false, "rewrite testdata/benchmark/baseline.json")

const (
	benchmarkDir      = "testdata/benchmark"
	benchmarkBaseline = "baseline.json"
	benchmarkSizeTol  = 0.02
	benchmarkSSIMTol  = 0.002
)

type benchmarkRecord struct {
	Case          string  `json:"case"`
	Class         string  `json:"class"`
	File          string  `json:"file"`
	Format        string  `json:"format"`
	Quality       int     `json:"quality"`
	TargetBytes   int64   `json:"targetBytes,omitempty"`
	TargetQuality int     `json:"targetQuality,omitempty"`
	Size          int64   `json:"size"`
	SSIM          float64 `json:"ssim"`
	SSIMMeasured  bool    `json:"ssimMeasured"`
	Missed        bool    `json:"missed,omitempty"`
}

type benchmarkBaselineFile struct {
	Note  string            `json:"note"`
	Cases []benchmarkRecord `json:"cases"`
}

type benchmarkCase struct {
	name     string
	class    string
	file     string
	format   string
	quality  int
	target   int64
	animated bool
}

// The set has at least two images in each class AnyPic compresses. The
// synthetic fixtures make the numbers reproducible; the real ones keep them
// honest (the app's own screenshot, project logo, and two public-domain NASA
// JPL photos). See testdata/benchmark/README.md for sources and licenses.
var benchmarkCases = []benchmarkCase{
	{name: "screenshot-synthetic-png", class: "screenshot", file: "screenshot.png", format: "png", quality: 82},
	{name: "screenshot-synthetic-webp", class: "screenshot", file: "screenshot.png", format: "webp", quality: 82},
	{name: "screenshot-real-png", class: "screenshot", file: "screenshot-real.png", format: "png", quality: 82},
	{name: "screenshot-real-webp", class: "screenshot", file: "screenshot-real.png", format: "webp", quality: 82},
	{name: "photo-synthetic-jpeg", class: "photo", file: "photo.jpg", format: "jpeg", quality: 82},
	{name: "photo-synthetic-webp", class: "photo", file: "photo.jpg", format: "webp", quality: 82},
	{name: "photo-synthetic-webp-target", class: "photo", file: "photo.jpg", format: "webp", quality: 82, target: 80 << 10},
	{name: "photo-earth-jpeg-passthrough", class: "photo", file: "photo-earth.jpg", format: "jpeg", quality: 82},
	{name: "photo-earth-jpeg-q60", class: "photo", file: "photo-earth.jpg", format: "jpeg", quality: 60},
	{name: "photo-earth-webp", class: "photo", file: "photo-earth.jpg", format: "webp", quality: 82},
	{name: "photo-earth2-jpeg-passthrough", class: "photo", file: "photo-earth2.jpg", format: "jpeg", quality: 82},
	{name: "photo-earth2-jpeg-q60", class: "photo", file: "photo-earth2.jpg", format: "jpeg", quality: 60},
	{name: "photo-earth2-webp", class: "photo", file: "photo-earth2.jpg", format: "webp", quality: 82},
	{name: "transparent-synthetic-auto", class: "transparent", file: "transparent.png", format: "auto", quality: 82},
	{name: "transparent-synthetic-webp", class: "transparent", file: "transparent.png", format: "webp", quality: 82},
	{name: "transparent-logo-auto", class: "transparent", file: "logo.png", format: "auto", quality: 82},
	{name: "transparent-logo-webp", class: "transparent", file: "logo.png", format: "webp", quality: 82},
	{name: "animated-synthetic-auto", class: "animation", file: "animated.gif", format: "auto", quality: 82, animated: true},
	{name: "animated-real-auto", class: "animation", file: "animated-real.gif", format: "auto", quality: 82, animated: true},
}

// TestBenchmarkCoversEveryClassAtLeastTwice keeps the set from shrinking back
// to one synthetic image per category: every class must keep at least two
// images, and the real screenshot/photo/logo fixtures must stay in place.
func TestBenchmarkCoversEveryClassAtLeastTwice(t *testing.T) {
	counts := map[string]int{}
	files := map[string]bool{}
	for _, c := range benchmarkCases {
		counts[c.class]++
		files[c.file] = true
	}
	for _, class := range []string{"screenshot", "photo", "transparent", "animation"} {
		if counts[class] < 2 {
			t.Errorf("class %q has %d images, want at least 2", class, counts[class])
		}
	}
	for _, real := range []string{"screenshot-real.png", "photo-earth.jpg", "photo-earth2.jpg", "logo.png", "animated-real.gif"} {
		if !files[real] {
			t.Errorf("real fixture %q is missing from the benchmark set", real)
		}
	}
}

// TestBenchmarkSet records the size and measured SSIM of the fixed set under
// the formats AnyPic offers. It is the tripwire for encoder changes: run it
// after touching quality.go, compressor.go's encode paths, or the encoder
// dependencies.
func TestBenchmarkSet(t *testing.T) {
	service := newTestService(t)
	records := make([]benchmarkRecord, 0, len(benchmarkCases))

	for i, c := range benchmarkCases {
		path := filepath.Join(benchmarkDir, c.file)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: %v (run `go run ./testdata/benchmark/generate`)", path, err)
		}
		srcFormat := displayFormat(strings.TrimPrefix(filepath.Ext(c.file), "."))
		result, err := service.compressOne(context.Background(), int64(i+1), path, srcFormat, c.file, c.animated, Settings{
			Format:     c.format,
			Quality:    c.quality,
			TargetSize: c.target,
			Suffix:     "-bench",
		})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		info, err := os.Stat(result.path)
		if err != nil {
			t.Fatalf("%s: result file: %v", c.name, err)
		}
		if info.Size() != result.size {
			t.Fatalf("%s: reported %d bytes, file is %d", c.name, result.size, info.Size())
		}
		if c.target > 0 && !result.targetMissed && result.size > c.target {
			t.Fatalf("%s: wrote %d bytes over its %d-byte target", c.name, result.size, c.target)
		}
		records = append(records, benchmarkRecord{
			Case:          c.name,
			Class:         c.class,
			File:          c.file,
			Format:        c.format,
			Quality:       c.quality,
			TargetBytes:   c.target,
			TargetQuality: result.targetQuality,
			Size:          result.size,
			SSIM:          result.ssim,
			SSIMMeasured:  result.ssimMeasured,
			Missed:        result.targetMissed,
		})
	}

	baselinePath := filepath.Join(benchmarkDir, benchmarkBaseline)
	if *updateBenchmark {
		data, err := json.MarshalIndent(benchmarkBaselineFile{
			Note:  "Recorded by TestBenchmarkSet. Regenerate with: go test -run TestBenchmarkSet -update-benchmark .",
			Cases: records,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(baselinePath, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s with %d cases", baselinePath, len(records))
		return
	}

	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("%v (record one with `go test -run TestBenchmarkSet -update-benchmark .`)", err)
	}
	var baseline benchmarkBaselineFile
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatalf("%s: %v", baselinePath, err)
	}
	want := make(map[string]benchmarkRecord, len(baseline.Cases))
	for _, record := range baseline.Cases {
		want[record.Case] = record
	}
	if len(want) != len(records) {
		t.Fatalf("baseline has %d cases, benchmark set has %d; record a new baseline", len(want), len(records))
	}

	for _, got := range records {
		before, ok := want[got.Case]
		if !ok {
			t.Fatalf("baseline has no case %q", got.Case)
		}
		delta := float64(got.Size-before.Size) / float64(max(1, before.Size))
		missed := ""
		if got.Missed {
			missed = "  (target missed)"
		}
		t.Logf("%-22s %7d B (%+6.2f%%)  SSIM %.4f%s",
			got.Case, got.Size, delta*100, got.SSIM, missed)
		if delta > benchmarkSizeTol {
			t.Errorf("%s: %d bytes, %.2f%% over the %d-byte baseline (limit %.0f%%)",
				got.Case, got.Size, delta*100, before.Size, benchmarkSizeTol*100)
		}
		if before.SSIM > 0 && got.SSIM > 0 && before.SSIM-got.SSIM > benchmarkSSIMTol {
			t.Errorf("%s: SSIM %.6f, down from %.6f by more than %.3f",
				got.Case, got.SSIM, before.SSIM, benchmarkSSIMTol)
		}
		if before.SSIMMeasured && !got.SSIMMeasured {
			t.Errorf("%s: quality is no longer measured", got.Case)
		}
		if got.Missed && !before.Missed {
			t.Errorf("%s: target %d is now missed (was reached in the baseline)", got.Case, got.TargetBytes)
		}
	}

	missing := make([]string, 0)
	for name := range want {
		found := false
		for _, record := range records {
			if record.Case == name {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("benchmark cases no longer produced: %v", missing)
	}
}
