package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"libre2sim/libre2"
)

// traceStore is the directory of traces the "trace" glucose model plays: one
// <name>.txt per FRAM dump (see libre2.ParseTrace). They are local files that
// git ignores: real sensors' data stays out of the repository.
type traceStore struct{ dir string }

// traceEntry is a trace as GET /traces lists it.
type traceEntry struct {
	Name string `json:"name"`
	// Label is the file's first comment line, or its name if it has none.
	Label         string `json:"label"`
	Minutes       int    `json:"minutes"`
	ReferenceMgdl *int   `json:"referenceMgdl,omitempty"`
	FirstMgdl     int    `json:"firstMgdl"`
	LastMgdl      int    `json:"lastMgdl"`
}

func newTraceEntry(name string, t *libre2.Trace) traceEntry {
	label := t.Label
	if label == "" {
		label = name
	}
	return traceEntry{Name: name, Label: label, Minutes: len(t.Series), ReferenceMgdl: t.ReferenceMgdl,
		FirstMgdl: int(t.Series[0]), LastMgdl: int(t.Series[len(t.Series)-1])}
}

// traceName is what a trace may be called: it is also its file name.
var traceName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// list is every trace in the directory, by name; a file that is not a valid
// trace is logged and left out. A missing directory has none.
func (s traceStore) list() ([]traceEntry, error) {
	out := []traceEntry{}
	files, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		name := strings.TrimSuffix(f.Name(), ".txt")
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".txt") || !traceName.MatchString(name) {
			continue
		}
		t, err := s.load(name)
		if err != nil {
			log.Printf("trace %s: %v", f.Name(), err)
			continue
		}
		out = append(out, newTraceEntry(name, t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// load reads and decodes the trace called name.
func (s traceStore) load(name string) (*libre2.Trace, error) {
	if !traceName.MatchString(name) {
		return nil, fmt.Errorf("trace name %q: want letters, digits, - and _ only", name)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, name+".txt"))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no trace called %s in %s", name, s.dir)
	}
	if err != nil {
		return nil, err
	}
	return libre2.ParseTrace(string(b))
}

// add checks text (a trace file, or only its base64) and saves it as name,
// replacing a trace of that name.
func (s traceStore) add(name, text string) (traceEntry, error) {
	if !traceName.MatchString(name) {
		return traceEntry{}, fmt.Errorf("trace name %q: want 1 to 64 letters, digits, - and _", name)
	}
	t, err := libre2.ParseTrace(text)
	if err != nil {
		return traceEntry{}, err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return traceEntry{}, err
	}
	text = strings.TrimSpace(text) + "\n"
	tmp := filepath.Join(s.dir, name+".txt.tmp")
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return traceEntry{}, err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, name+".txt")); err != nil {
		return traceEntry{}, err
	}
	return newTraceEntry(name, t), nil
}

// defaultTracesDir is traces/ beside the libre2sim binary.
func defaultTracesDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "traces"
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Join(filepath.Dir(exe), "traces")
}
