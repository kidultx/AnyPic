package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestItemOwnsOneImmutableOutcome(t *testing.T) {
	itemType := reflect.TypeFor[Item]()
	field, found := itemType.FieldByName("Outcome")
	if !found || field.Type.Kind() != reflect.Pointer || field.Type.Elem().Name() != "Outcome" {
		t.Fatal("Item must hold *Outcome; nil means no compression result")
	}
	for _, name := range []string{
		"CompressedSize", "Error", "OutputFormat", "OutputName", "PassedThrough",
		"KeptOriginalReason", "Metadata", "TransparencyKept", "SavedPercent",
		"SSIM", "SSIMMeasured", "LargeImageFastMode", "TargetBytes", "TargetQuality", "TargetMissed",
	} {
		if _, found := itemType.FieldByName(name); found {
			t.Errorf("Item still duplicates outcome field %s", name)
		}
	}
	outcomeType := field.Type.Elem()
	for index := range outcomeType.NumField() {
		if field := outcomeType.Field(index); field.IsExported() {
			t.Errorf("Outcome.%s must not expose mutable state", field.Name)
		}
	}
}

func TestOutcomePassthroughAlwaysHasReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.png")
	size := writePNG(t, path, gradientImage(12, 8, true))
	for _, reason := range []OutcomeReason{ReasonNotSmaller, ReasonTargetMet, ReasonPixelsChanged, ReasonAnimatedPNG, ReasonColorSpace} {
		t.Run(string(reason), func(t *testing.T) {
			outcome := newPassedThroughOutcome(path, "source.png", "PNG", Settings{Suffix: "-anypic", TargetSize: size}, reason, OutcomeSnapshot{SSIMMeasured: true})
			result := outcome.Snapshot()
			if outcome.Kind() != OutcomePassedThrough || outcome.Reason() != reason || outcome.Err() != nil {
				t.Fatalf("passthrough = %+v, error = %v", result, outcome.Err())
			}
			if result.CompressedSize != size || result.OutputName != "source-anypic.png" || outcome.Path() != path || result.TargetMissed {
				t.Fatalf("original file facts changed: %+v", result)
			}
			if result.SSIM != 1 || !result.SSIMMeasured || result.Metadata != nil {
				t.Fatalf("passthrough invented processing: %+v", result)
			}
		})
	}
}

func TestOutcomeRejectsInvalidStates(t *testing.T) {
	for _, reason := range []OutcomeReason{ReasonNone, ReasonCanceled, ReasonUnknown, OutcomeReason("invented")} {
		t.Run("passthrough/"+string(reason), func(t *testing.T) {
			mustPanic(t, func() {
				newPassedThroughOutcome("", "", "PNG", Settings{}, reason, OutcomeSnapshot{})
			})
		})
	}
	for _, result := range []OutcomeSnapshot{
		{Kind: OutcomePassedThrough},
		{Kind: OutcomeEncoded, Reason: ReasonNotSmaller},
		{Kind: OutcomeFailed, Reason: ReasonTargetMet},
		{Kind: OutcomeCanceled, Reason: ReasonReadOriginal},
		{Kind: OutcomeKind("invented"), Reason: ReasonUnknown},
	} {
		t.Run(string(result.Kind)+"/"+string(result.Reason), func(t *testing.T) {
			mustPanic(t, func() { newOutcome("", result) })
		})
	}
	mustPanic(t, func() { newEncodedOutcome("", 0, OutcomeSnapshot{}) })
	mustPanic(t, func() { newFailedOutcome(nil) })
}

func TestOutcomeSnapshotsHaveValueSemantics(t *testing.T) {
	metadata := &MetadataReport{ICCKept: true}
	outcome := newEncodedOutcome("result.png", 200, OutcomeSnapshot{
		CompressedSize: 100, OutputFormat: "PNG", OutputName: "source-anypic.png",
		Width: 12, Height: 8, Metadata: metadata,
	})
	metadata.ICCKept = false
	first := outcome.Snapshot()
	first.Metadata.ICCKept = false
	first.OutputName = "modified.png"
	copy := *outcome
	second := copy.Snapshot()
	if !second.Metadata.ICCKept || second.OutputName != "source-anypic.png" || second.SavedPercent != 50 {
		t.Fatalf("mutable alias escaped the outcome: %+v", second)
	}
	if outcome.Kind() != OutcomeEncoded || outcome.Reason() != ReasonNone {
		t.Fatalf("encoded outcome = %s/%s", outcome.Kind(), outcome.Reason())
	}
}

func TestOutcomeFailuresStayStructured(t *testing.T) {
	for _, test := range []struct {
		err    error
		kind   OutcomeKind
		reason OutcomeReason
	}{
		{compressionError{reason: ReasonReadOriginal}, OutcomeFailed, ReasonReadOriginal},
		{context.Canceled, OutcomeCanceled, ReasonCanceled},
		{errors.New("encoder diagnostic"), OutcomeFailed, ReasonUnknown},
	} {
		outcome := newFailedOutcome(test.err)
		result := outcome.Snapshot()
		if outcome.Kind() != test.kind || outcome.Reason() != test.reason || outcome.Successful() || outcome.Err() == nil {
			t.Fatalf("failure = %+v, error = %v", result, outcome.Err())
		}
		if outcome.Path() != "" || result.CompressedSize != 0 || result.OutputFormat != "" || result.Metadata != nil {
			t.Fatalf("failure retains success facts: %+v", result)
		}
		if test.kind == OutcomeCanceled && !errors.Is(outcome.Err(), context.Canceled) {
			t.Fatal("cancellation lost its sentinel")
		}
	}
	original := uiLanguage()
	defer setUILanguage(original)
	outcome := newFailedOutcome(compressionError{reason: ReasonReadOriginal})
	setUILanguage("en")
	english := outcome.Err().Error()
	setUILanguage("zh-Hans")
	if outcome.Err().Error() == english || outcome.Snapshot().ErrorDetail != "" {
		t.Fatal("failure text was burned into the outcome before rendering")
	}
	zero := Outcome{}
	if zero.Kind() != OutcomeFailed || zero.Reason() != ReasonUnknown || zero.Err() == nil {
		t.Fatal("the externally constructible zero value must be a safe unknown failure")
	}
}

func mustPanic(t *testing.T, action func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("an invalid outcome was constructible")
		}
	}()
	action()
}

func outcomeWithError(outcome *Outcome) (*Outcome, error) {
	return outcome, outcome.Err()
}

func TestOutcomeResetToNil(t *testing.T) {
	for _, kind := range []OutcomeKind{OutcomeEncoded, OutcomePassedThrough, OutcomeFailed, OutcomeCanceled} {
		t.Run(string(kind), func(t *testing.T) {
			service := newTestService(t)
			source := filepath.Join(t.TempDir(), "source.png")
			originalSize := writePNG(t, source, gradientImage(12, 8, true))
			original, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			resultPath := filepath.Join(service.tempDir, "result.png")
			resultSize := writePNG(t, resultPath, gradientImage(6, 4, true))
			var outcome *Outcome
			switch kind {
			case OutcomeEncoded:
				outcome = newEncodedOutcome(resultPath, originalSize, OutcomeSnapshot{
					CompressedSize: resultSize, OutputFormat: "PNG", OutputName: "source-anypic.png", Width: 6, Height: 4,
				})
			case OutcomePassedThrough:
				outcome = newPassedThroughOutcome(source, "source.png", "PNG", service.settings, ReasonNotSmaller, OutcomeSnapshot{SSIMMeasured: true})
			case OutcomeFailed:
				outcome = newFailedOutcome(compressionError{reason: ReasonDecodeCorrupt})
			case OutcomeCanceled:
				outcome = newFailedOutcome(context.Canceled)
			}
			item := &Item{Path: source, Outcome: outcome, saved: true, savedPath: "previous-export.png"}
			before := item.snapshot()
			service.resetOutcome(item)
			service.resetOutcome(item)
			if item.Outcome != nil || item.saved || item.savedPath != "" || item.snapshot().Outcome != nil {
				t.Fatalf("reset left result state: %+v", item)
			}
			if before.Outcome.Kind != kind || outcome.Kind() != kind {
				t.Fatal("reset mutated a previously published outcome")
			}
			if kind == OutcomeEncoded {
				if _, err := os.Stat(resultPath); !os.IsNotExist(err) {
					t.Fatalf("reset did not remove the obsolete temporary result: %v", err)
				}
			}
			after, err := os.ReadFile(source)
			if err != nil || !reflect.DeepEqual(original, after) {
				t.Fatal("reset removed or changed the original")
			}
		})
	}
}

func TestOutcomeBatchStartResetsQueuedAndWorkingItems(t *testing.T) {
	service := newTestService(t)
	directory := t.TempDir()
	for index := range 4 {
		writePNG(t, filepath.Join(directory, fmt.Sprintf("source-%d.png", index)), gradientImage(12, 8, true))
	}
	if _, err := service.AddPaths([]string{directory}); err != nil {
		t.Fatal(err)
	}
	oldPaths := make([]string, 0, 4)
	service.mu.Lock()
	for _, item := range service.items {
		path := filepath.Join(service.tempDir, fmt.Sprintf("old-%d.png", item.ID))
		size := writePNG(t, path, gradientImage(6, 4, true))
		item.Outcome = newEncodedOutcome(path, item.OriginalSize, OutcomeSnapshot{
			CompressedSize: size, OutputFormat: "PNG", OutputName: "source-anypic.png", Width: 6, Height: 4,
		})
		item.Status = statusDone
		item.saved, item.savedPath = true, "previous-export.png"
		oldPaths = append(oldPaths, path)
	}
	service.mu.Unlock()
	previous := pngLosslessEncode
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	pngLosslessEncode = func(img image.Image) ([]byte, bool, error) {
		entered <- struct{}{}
		<-release
		return previous(img)
	}
	defer func() {
		close(release)
		waitFor(t, "outcome reset pass", func() bool { return !service.State().Batch.Running })
		pngLosslessEncode = previous
	}()
	settings := service.settings
	settings.Format = "png"
	if err := service.Compress(settings); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker never reached the encoder")
	}
	state := service.State()
	for _, item := range state.Items {
		if item.Outcome != nil || (item.Status != statusReady && item.Status != statusWorking) {
			t.Fatalf("queued or working item carries a previous result: %+v", item)
		}
	}
	service.mu.Lock()
	for _, item := range service.items {
		if item.Outcome != nil || item.saved || item.savedPath != "" {
			service.mu.Unlock()
			t.Fatal("the batch did not reset all of its queued outcomes and save receipts")
		}
	}
	service.mu.Unlock()
	for _, path := range oldPaths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("an obsolete temporary result survived reset: %v", err)
		}
	}
}

func TestOutcomeDecorateResetsToNil(t *testing.T) {
	service := newTestService(t)
	source := filepath.Join(t.TempDir(), "source.png")
	writePNG(t, source, gradientImage(12, 8, true))
	added, err := service.AddPaths([]string{source})
	if err != nil {
		t.Fatal(err)
	}
	id := added.Snapshot.Items[0].ID
	service.compressItem(context.Background(), id, service.settings)
	before := service.State().Items[0].Outcome
	if before == nil {
		t.Fatal("the fixture has no completed outcome")
	}
	state, err := service.DecorateItem(id, FrameStyle{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Items[0].Outcome != nil || state.Items[0].Status != statusReady {
		t.Fatal("beautify retained the compression result of the previous source")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("beautify removed the original: %v", err)
	}
}

func TestOutcomeFailureAndCancellationReplaceThePreviousResult(t *testing.T) {
	service := newTestService(t)
	source := filepath.Join(t.TempDir(), "source.png")
	writePNG(t, source, gradientImage(12, 8, false))
	added, err := service.AddPaths([]string{source})
	if err != nil {
		t.Fatal(err)
	}
	id := added.Snapshot.Items[0].ID
	service.compressItem(context.Background(), id, service.settings)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	service.compressItem(context.Background(), id, service.settings)
	failed := service.State().Items[0]
	if failed.Status != statusError || failed.Outcome.Kind != OutcomeFailed || failed.Outcome.Reason != ReasonReadOriginal || failed.Outcome.CompressedSize != 0 {
		t.Fatalf("failed pass retained old facts: %+v", failed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service.compressItem(ctx, id, service.settings)
	canceled := service.State().Items[0]
	if canceled.Status != statusError || canceled.Outcome.Kind != OutcomeCanceled || canceled.Outcome.Reason != ReasonCanceled {
		t.Fatalf("canceled pass = %+v", canceled)
	}
	if failed.Outcome.Kind != OutcomeFailed {
		t.Fatal("a later pass mutated a published failure")
	}
}

func TestOutcomeIPCSnapshotIsDetachedAndNested(t *testing.T) {
	service := newTestService(t)
	item := &Item{ID: 1, Path: "source.png", Status: statusDone, Outcome: newEncodedOutcome("private-result.png", 200, OutcomeSnapshot{
		CompressedSize: 100, OutputFormat: "PNG", OutputName: "source-anypic.png", Width: 12, Height: 8,
		Metadata: &MetadataReport{ICCKept: true},
	})}
	service.items = []*Item{item}
	service.byID[item.ID] = item
	first := service.State().Items[0]
	first.Outcome.Metadata.ICCKept = false
	first.Outcome.OutputName = "mutated.png"
	second := service.State().Items[0]
	if !second.Outcome.Metadata.ICCKept || second.Outcome.OutputName != "source-anypic.png" {
		t.Fatal("an IPC reader can mutate the worker's outcome")
	}
	raw, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"compressedSize", "outputFormat", "outputName", "passedThrough", "keptOriginalReason", "error", "compressedPath"} {
		if _, found := decoded[removed]; found {
			t.Errorf("IPC still duplicates %s outside outcome", removed)
		}
	}
	var result OutcomeSnapshot
	if err := json.Unmarshal(decoded["outcome"], &result); err != nil || !reflect.DeepEqual(result, *second.Outcome) {
		t.Fatalf("IPC lost outcome facts: %s (%v)", raw, err)
	}
	item.Outcome = nil
	raw, err = json.Marshal(item.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || string(decoded["outcome"]) != "null" {
		t.Fatalf("an uncompressed item must serialize outcome:null: %s (%v)", raw, err)
	}
}
