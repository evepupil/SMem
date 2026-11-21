package memory

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smem/internal/workspace"
)

func lifecycleLayout(t *testing.T) workspace.Layout {
	t.Helper()
	root := t.TempDir()
	layout := workspace.Layout{
		Home:    filepath.Join(root, "home"),
		Project: filepath.Join(root, "project-a"),
	}
	for _, dir := range []string{layout.Home, layout.Project, filepath.Join(layout.Home, ".mem", ".git")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return layout
}

func lifecycleRecord(t *testing.T, layout workspace.Layout, scope, text string) Record {
	t.Helper()
	records, _, err := List(layout, scope, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Text == text {
			return record
		}
	}
	t.Fatalf("record %q not found in scope %q", text, scope)
	return Record{}
}

func TestLifecycleReferencesAreBoundToProjectAndScope(t *testing.T) {
	layoutA := lifecycleLayout(t)
	layoutB := layoutA
	layoutB.Project = filepath.Join(filepath.Dir(layoutA.Project), "project-b")
	if err := os.MkdirAll(layoutB.Project, 0700); err != nil {
		t.Fatal(err)
	}

	const sameText = "crossproject release uses cedar protocol"
	pathA, created, err := AddNote(layoutA, false, false, sameText)
	if err != nil || !created {
		t.Fatalf("add project A note: created=%v err=%v", created, err)
	}
	pathB, created, err := AddNote(layoutB, false, false, sameText)
	if err != nil || !created {
		t.Fatalf("add project B note: created=%v err=%v", created, err)
	}
	if filepath.Base(pathA) != filepath.Base(pathB) {
		t.Fatalf("same content should have the same managed filename: %q != %q", pathA, pathB)
	}

	projectRecord := lifecycleRecord(t, layoutA, "project", sameText)
	otherProjectRecord := lifecycleRecord(t, layoutB, "project", sameText)
	if projectRecord.Ref == otherProjectRecord.Ref {
		t.Fatal("identical managed filenames in different projects must have distinct references")
	}
	if _, err := Show(layoutB, projectRecord.Ref); err == nil {
		t.Fatal("reference from project A resolved in project B")
	}
	if err := Update(layoutB, projectRecord.Ref, "must not cross project boundaries"); err == nil {
		t.Fatal("update accepted a reference from another project")
	}
	if err := Forget(layoutB, projectRecord.Ref); err == nil {
		t.Fatal("forget accepted a reference from another project")
	}
	for _, path := range []string{pathA, pathB} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(sameText)) {
			t.Fatalf("cross-project mutation changed %s: %q", path, data)
		}
	}

	globalPath, created, err := AddNote(layoutA, true, false, sameText)
	if err != nil || !created {
		t.Fatalf("add global note: created=%v err=%v", created, err)
	}
	globalRecord := lifecycleRecord(t, layoutA, "global", sameText)
	if filepath.Base(globalPath) != filepath.Base(pathA) || globalRecord.Ref == projectRecord.Ref {
		t.Fatalf("scope was not bound into the reference: project=%#v global=%#v", projectRecord, globalRecord)
	}
	if got, err := Show(layoutA, projectRecord.Ref); err != nil || got.Scope != "project" {
		t.Fatalf("project reference resolved to the wrong scope: record=%#v err=%v", got, err)
	}
	if got, err := Show(layoutA, globalRecord.Ref); err != nil || got.Scope != "global" {
		t.Fatalf("global reference resolved to the wrong scope: record=%#v err=%v", got, err)
	}

	projectBRecords, _, err := List(layoutB, "project", 0, 100)
	if err != nil || len(projectBRecords) != 1 || projectBRecords[0].Ref != otherProjectRecord.Ref {
		t.Fatalf("project B received project A memory: records=%#v err=%v", projectBRecords, err)
	}
	allBRecords, _, err := List(layoutB, "all", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sawGlobal bool
	for _, record := range allBRecords {
		if record.Scope == "global" && record.Text == sameText {
			sawGlobal = true
		}
		if record.Scope == "project" && record.Ref == projectRecord.Ref {
			t.Fatal("project A memory leaked into project B")
		}
	}
	if !sawGlobal {
		t.Fatal("global user memory was not shared across projects")
	}
}

func TestLifecycleManualEditInvalidatesReadReference(t *testing.T) {
	layout := lifecycleLayout(t)
	path, _, err := AddNote(layout, false, false, "original managed note")
	if err != nil {
		t.Fatal(err)
	}
	old := lifecycleRecord(t, layout, "project", "original managed note")
	if _, err := Show(layout, old.Ref); err != nil {
		t.Fatalf("read initial reference: %v", err)
	}
	manual := []byte("hand-edited replacement\n")
	if err := os.WriteFile(path, manual, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Show(layout, old.Ref); err == nil {
		t.Fatal("old reference remained valid after a manual edit")
	}
	if err := Update(layout, old.Ref, "overwrite attempt"); err == nil {
		t.Fatal("update accepted a stale reference")
	}
	if err := Forget(layout, old.Ref); err == nil {
		t.Fatal("forget accepted a stale reference")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, manual) {
		t.Fatalf("stale reference changed manually edited file: data=%q err=%v", got, err)
	}
}

func TestLifecycleTopicMutationPreservesBOMCRLFAndAdjacentLines(t *testing.T) {
	layout := lifecycleLayout(t)
	store, err := layout.Store(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Path(), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Path(), "topic.md")
	original := append([]byte{0xef, 0xbb, 0xbf}, []byte("# Topic\r\nalpha stays\r\nbeta changes\r\ngamma stays\r\n")...)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}

	first := lifecycleRecord(t, layout, "project", "beta changes")
	if err := Update(layout, first.Ref, "beta corrected"); err != nil {
		t.Fatalf("update topic row: %v", err)
	}
	wantAfterUpdate := append([]byte{0xef, 0xbb, 0xbf}, []byte("# Topic\r\nalpha stays\r\nbeta corrected\r\ngamma stays\r\n")...)
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, wantAfterUpdate) {
		t.Fatalf("topic update changed bytes outside its row: got=%q err=%v", got, err)
	}

	second := lifecycleRecord(t, layout, "project", "alpha stays")
	if err := Forget(layout, second.Ref); err != nil {
		t.Fatalf("forget topic row: %v", err)
	}
	wantAfterForget := append([]byte{0xef, 0xbb, 0xbf}, []byte("# Topic\r\nbeta corrected\r\ngamma stays\r\n")...)
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(got, wantAfterForget) {
		t.Fatalf("topic deletion changed bytes outside its row: got=%q err=%v", got, err)
	}
}

func TestLifecycleCorrectionRemovesOldSearchAndAddDoesNotDuplicate(t *testing.T) {
	layout := lifecycleLayout(t)
	_, created, err := AddNote(layout, false, false, "legacyterm amber release note")
	if err != nil || !created {
		t.Fatalf("add original note: created=%v err=%v", created, err)
	}
	old := lifecycleRecord(t, layout, "project", "legacyterm amber release note")
	const correctedText = "newterm cobalt release note"
	if err := Update(layout, old.Ref, correctedText); err != nil {
		t.Fatalf("correct managed note: %v", err)
	}

	oldMatches, err := Related(layout, "legacyterm")
	if err != nil || len(oldMatches) != 0 {
		t.Fatalf("old text still matches after correction: records=%#v err=%v", oldMatches, err)
	}
	newMatches, err := Related(layout, "newterm")
	if err != nil || len(newMatches) != 1 || newMatches[0].Text != correctedText {
		t.Fatalf("corrected text is not uniquely searchable: records=%#v err=%v", newMatches, err)
	}

	path, created, err := AddNote(layout, false, false, correctedText)
	if err != nil || created {
		t.Fatalf("adding corrected note should reuse it: path=%q created=%v err=%v", path, created, err)
	}
	records, _, err := List(layout, "project", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, record := range records {
		if record.Text == correctedText {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("corrected note appears %d times", count)
	}
}

func TestLifecyclePinnedAndRelatedLimitsAreIndependent(t *testing.T) {
	layout := lifecycleLayout(t)
	for i := 0; i < pinnedLimit+2; i++ {
		text := strings.Join([]string{"quasar", "common", string(rune('a' + i))}, " ")
		if _, _, err := AddNote(layout, false, true, text); err != nil {
			t.Fatalf("add pinned note %d: %v", i, err)
		}
	}
	for i := 0; i < relatedLimit+3; i++ {
		text := strings.Join([]string{"quasar", "related", string(rune('a' + i))}, " ")
		if _, _, err := AddNote(layout, false, false, text); err != nil {
			t.Fatalf("add related note %d: %v", i, err)
		}
	}

	pinned, more, matches, err := Start(layout, "quasar")
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned) != pinnedLimit || !more {
		t.Fatalf("pinned limit not enforced independently: len=%d more=%v", len(pinned), more)
	}
	if len(matches) != relatedLimit {
		t.Fatalf("related limit was crowded out by common notes: got %d want %d", len(matches), relatedLimit)
	}
	pinnedRefs := make(map[string]bool, len(pinned))
	for _, record := range pinned {
		if !record.Always {
			t.Fatalf("non-common record in pinned results: %#v", record)
		}
		pinnedRefs[record.Ref] = true
	}
	for _, record := range matches {
		if record.Always || pinnedRefs[record.Ref] {
			t.Fatalf("common result crowded into related results: %#v", record)
		}
	}
	related, err := Related(layout, "quasar")
	if err != nil || len(related) != relatedLimit {
		t.Fatalf("standalone related limit: got %d err=%v", len(related), err)
	}
}
