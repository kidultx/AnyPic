package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
)

// unsavedQuitFixture builds a service holding one finished result that lives
// only in the session temp dir — exactly the state a quit used to delete
// silently.
func unsavedQuitFixture(t *testing.T) *AnyPic {
	t.Helper()
	service := newTestService(t)
	comp := filepath.Join(service.tempDir, "1-result.webp")
	if err := os.WriteFile(comp, []byte("result"), 0o600); err != nil {
		t.Fatal(err)
	}
	item := &Item{
		ID:             7,
		Name:           "photo.jpg",
		Path:           filepath.Join(t.TempDir(), "photo.jpg"),
		Dir:            t.TempDir(),
		Format:         "JPEG",
		Status:         statusDone,
		OutputFormat:   "WEBP",
		OutputName:     "photo-anypic.webp",
		compressedPath: comp,
	}
	service.items = []*Item{item}
	service.byID = map[int64]*Item{item.ID: item}
	return service
}

// TestQuitGuardHoldsBackUnsavedResults: a quit with compressed results that
// exist only in the session temp dir must not run Cleanup before the page
// has explained what would be lost. Once a durable copy exists, the same
// quit proceeds.
func TestQuitGuardHoldsBackUnsavedResults(t *testing.T) {
	service := unsavedQuitFixture(t)

	var shown bool
	var notified string
	e := &mygo.QuitEvent{}
	if beforeQuit(service, func() { shown = true }, func(command string) { notified = command }, e) {
		t.Fatal("quit proceeded while its results lived only in the session temp dir")
	}
	if !e.DefaultPrevented() {
		t.Error("quit was not prevented")
	}
	if !shown {
		t.Error("the window was not brought forward for the confirmation")
	}
	if notified != "quit-unsaved" {
		t.Errorf("page command = %q, want quit-unsaved", notified)
	}

	// An export writes a durable copy; from then on quitting loses nothing.
	service.markResultsSaved([]savedCopy{{id: 7, path: filepath.Join(t.TempDir(), "photo-anypic.webp")}})
	e = &mygo.QuitEvent{}
	if !beforeQuit(service, func() { t.Error("window shown with nothing to lose") }, nil, e) {
		t.Fatal("quit was held back after the result was exported")
	}
	if e.DefaultPrevented() {
		t.Error("quit with saved results was prevented")
	}
}

// TestQuitGuardLetsAConfirmedQuitThrough: QuitNow is how the page resumes the
// quit it interrupted; the guard must not interrupt it forever.
func TestQuitGuardLetsAConfirmedQuitThrough(t *testing.T) {
	service := unsavedQuitFixture(t)
	service.quitConfirmed.Store(true)

	e := &mygo.QuitEvent{}
	if !beforeQuit(service, func() { t.Error("window shown after the user confirmed") }, nil, e) {
		t.Fatal("a confirmed quit was held back")
	}
	if e.DefaultPrevented() {
		t.Error("a confirmed quit was prevented")
	}
}

// TestQuitNowQuitsAndRecordsConfirmation: the page's "quit anyway" and the
// successful "export first" both end here; quitting must go through the
// normal quit path (quitApp) with the confirmation recorded.
func TestQuitNowQuitsAndRecordsConfirmation(t *testing.T) {
	service := newTestService(t)
	old := quitApp
	quit := false
	quitApp = func() { quit = true }
	t.Cleanup(func() { quitApp = old })

	service.QuitNow()
	if !service.quitConfirmed.Load() {
		t.Error("QuitNow did not record the confirmation")
	}
	if !quit {
		t.Error("QuitNow did not request the quit")
	}
}
