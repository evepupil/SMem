package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"smem/internal/memory"
	"smem/internal/workspace"
)

func lifecycleCLILayout(t *testing.T) workspace.Layout {
	t.Helper()
	root := t.TempDir()
	layout := workspace.Layout{
		Home:    filepath.Join(root, "home"),
		Project: filepath.Join(root, "project"),
	}
	for _, dir := range []string{layout.Home, layout.Project, filepath.Join(layout.Home, ".mem", ".git")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return layout
}

func lifecycleCLIRecord(t *testing.T, layout workspace.Layout, text string) memory.Record {
	t.Helper()
	records, _, err := memory.List(layout, "project", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Text == text {
			return record
		}
	}
	t.Fatalf("record %q not found", text)
	return memory.Record{}
}

func snapshotCLIFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state := info.Mode().String()
		if entry.IsDir() {
			files[rel] = state
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = state + "\x00" + string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertRecordRendered(t *testing.T, output string, record memory.Record) {
	t.Helper()
	for _, field := range []string{record.Ref, record.Scope, record.Path, strconv.Itoa(record.Line), record.Text} {
		if !strings.Contains(output, field) {
			t.Errorf("CLI output does not display record field %q: %q", field, output)
		}
	}
}

func TestLifecycleListPagesAreRenderedAndReadOnly(t *testing.T) {
	layout := lifecycleCLILayout(t)
	texts := []string{"pagealpha first record", "pagebravo second record", "pagecharlie third record"}
	for _, text := range texts {
		if _, _, err := memory.AddNote(layout, false, false, text); err != nil {
			t.Fatal(err)
		}
	}
	records, _, err := memory.List(layout, "project", 0, 100)
	if err != nil || len(records) != len(texts) {
		t.Fatalf("read list ordering: records=%#v err=%v", records, err)
	}
	before := snapshotCLIFiles(t, filepath.Join(layout.Project, ".mem"))

	var first bytes.Buffer
	if err := Run([]string{"list", "--scope", "project", "--offset", "0", "--limit", "2"}, layout, &first, io.Discard); err != nil {
		t.Fatalf("first list page: %v", err)
	}
	assertRecordRendered(t, first.String(), records[0])
	assertRecordRendered(t, first.String(), records[1])
	for _, absent := range records[2:] {
		if strings.Contains(first.String(), absent.Ref) || strings.Contains(first.String(), absent.Text) {
			t.Errorf("first page included a later record: %q", first.String())
		}
	}

	var second bytes.Buffer
	if err := Run([]string{"list", "--scope", "project", "--offset", "2", "--limit", "2"}, layout, &second, io.Discard); err != nil {
		t.Fatalf("second list page: %v", err)
	}
	assertRecordRendered(t, second.String(), records[2])
	for _, absent := range records[:2] {
		if strings.Contains(second.String(), absent.Ref) || strings.Contains(second.String(), absent.Text) {
			t.Errorf("second page repeated a prior record: %q", second.String())
		}
	}
	if after := snapshotCLIFiles(t, filepath.Join(layout.Project, ".mem")); !reflect.DeepEqual(before, after) {
		t.Fatalf("list changed files: before=%#v after=%#v", before, after)
	}
}

func TestLifecycleListRejectsInvalidPagination(t *testing.T) {
	layout := lifecycleCLILayout(t)
	cases := []struct {
		name string
		args []string
	}{
		{name: "negative offset", args: []string{"list", "--offset", "-1"}},
		{name: "non-numeric offset", args: []string{"list", "--offset", "one"}},
		{name: "zero limit", args: []string{"list", "--limit", "0"}},
		{name: "limit over maximum", args: []string{"list", "--limit", "101"}},
		{name: "unknown scope", args: []string{"list", "--scope", "brand"}},
		{name: "unexpected positional", args: []string{"list", "extra"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := Run(test.args, layout, io.Discard, io.Discard); err == nil {
				t.Fatalf("accepted invalid list arguments %v", test.args)
			}
		})
	}
}

func TestLifecycleShowAndMutationRequireExactReference(t *testing.T) {
	layout := lifecycleCLILayout(t)
	const originalText = "single-record show amber protocol"
	if _, _, err := memory.AddNote(layout, false, false, originalText); err != nil {
		t.Fatal(err)
	}
	otherText := "unrelated record violet protocol"
	if _, _, err := memory.AddNote(layout, false, false, otherText); err != nil {
		t.Fatal(err)
	}
	original := lifecycleCLIRecord(t, layout, originalText)
	other := lifecycleCLIRecord(t, layout, otherText)

	var shown bytes.Buffer
	if err := Run([]string{"show", original.Ref}, layout, &shown, io.Discard); err != nil {
		t.Fatalf("show exact reference: %v", err)
	}
	assertRecordRendered(t, shown.String(), original)
	if strings.Contains(shown.String(), other.Ref) || strings.Contains(shown.String(), otherText) {
		t.Fatalf("show rendered more than the selected record: %q", shown.String())
	}

	const correctedText = "corrected single record indigo protocol"
	var updated bytes.Buffer
	if err := Run([]string{"update", original.Ref, correctedText}, layout, &updated, io.Discard); err != nil {
		t.Fatalf("update exact reference: %v", err)
	}
	if _, err := memory.Show(layout, original.Ref); err == nil {
		t.Fatal("old reference remained usable after CLI update")
	}
	corrected := lifecycleCLIRecord(t, layout, correctedText)
	if _, err := memory.Show(layout, corrected.Ref); err != nil {
		t.Fatalf("updated record is not readable: %v", err)
	}

	currentData, err := os.ReadFile(corrected.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "update by path", args: []string{"update", corrected.Path, "unsafe path update"}},
		{name: "update by keyword", args: []string{"update", corrected.Text, "unsafe keyword update"}},
		{name: "forget by path", args: []string{"forget", corrected.Path}},
		{name: "forget by keyword", args: []string{"forget", corrected.Text}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := Run(test.args, layout, io.Discard, io.Discard); err == nil {
				t.Fatalf("accepted non-reference mutation selector %v", test.args)
			}
			got, err := os.ReadFile(corrected.Path)
			if err != nil || !bytes.Equal(got, currentData) {
				t.Fatalf("rejected selector changed note: data=%q err=%v", got, err)
			}
		})
	}

	if err := Run([]string{"forget", corrected.Ref}, layout, io.Discard, io.Discard); err != nil {
		t.Fatalf("forget exact reference: %v", err)
	}
	if _, err := memory.Show(layout, corrected.Ref); err == nil {
		t.Fatal("forgotten record remained visible")
	}
	if _, err := memory.Show(layout, other.Ref); err != nil {
		t.Fatalf("mutation affected an unrelated record: %v", err)
	}
}
