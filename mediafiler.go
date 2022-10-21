package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base32"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zncoder/qad"
)

type FileInfo struct {
	Path    string
	ID      string
	ModTime time.Time
}

type Dirs struct {
	dirs     []string
	suffixes []string

	mu        sync.Mutex
	files     []FileInfo           // sorted
	toDel     map[string]time.Time // path -> time deleted
	toArchive map[string]time.Time // path -> time archived
}

func New(dirs, suffixes []string) *Dirs {
	ds := &Dirs{
		dirs:      dirs,
		suffixes:  suffixes,
		toDel:     make(map[string]time.Time),
		toArchive: make(map[string]time.Time),
	}
	ds.discoverGarbage()
	return ds
}

//go:embed asset
var asset embed.FS

var indexTmpl = func() *template.Template {
	index, err := asset.ReadFile("asset/index.html")
	qad.Assert(err, "asset/index.html")
	funcMap := template.FuncMap{
		"title": extractTitle,
	}
	return template.Must(template.New("index").Funcs(funcMap).Parse(string(index)))
}()

func extractTitle(p string) string {
	name := filepath.Base(p)
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext)
}

func (ds *Dirs) Index(w http.ResponseWriter, r *http.Request) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.refresh()

	data := struct {
		EnableArchive bool
		Files         []FileInfo
	}{archiveDir != "", ds.files}

	var bb bytes.Buffer
	err := indexTmpl.Execute(&bb, data)
	qad.Assert(err, "exec template")
	w.Write(bb.Bytes())
}

func (ds *Dirs) getFileByID(id string) (string, bool) {
	for _, fi := range ds.files {
		if fi.ID == id {
			return fi.Path, true
		}
	}
	return "", false
}

func (ds *Dirs) ArchiveFile(w http.ResponseWriter, r *http.Request) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if archiveDir == "" {
		http.Error(w, "Archive not supported", http.StatusBadRequest)
		return
	}

	ds.archiveOrDeleteFile(w, r, "archive")
}

func (ds *Dirs) DeleteFile(w http.ResponseWriter, r *http.Request) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.archiveOrDeleteFile(w, r, "delete")
}

func (ds *Dirs) archiveOrDeleteFile(w http.ResponseWriter, r *http.Request, action string) {
	fid := filepath.Base(r.URL.Path)
	_, undo := r.URL.Query()["undo"]

	filename, ok := ds.getFileByID(fid)
	if !ok {
		http.Error(w, "Invalid id to "+action, http.StatusBadRequest)
		return
	}

	from := filename
	to := filename + "." + action
	if undo {
		from, to = to, from
	}

	err := os.Rename(from, to)
	if err != nil {
		http.Error(w, "Rename error", http.StatusInternalServerError)
		return
	}

	todo := ds.toDel
	if action == "archive" {
		todo = ds.toArchive
	}

	if undo {
		delete(todo, from)
	} else {
		todo[to] = time.Now()
	}
}

func (ds *Dirs) runDeleter() {
	threshold := 11 * time.Minute
	for now := range time.Tick(threshold) {
		stayTs := now.Add(-threshold)

		var todel, toarchive []string
		ds.mu.Lock()
		for p, ts := range ds.toDel {
			if ts.Before(stayTs) {
				delete(ds.toDel, p)
				todel = append(todel, p)
			}
		}
		for p, ts := range ds.toArchive {
			if ts.Before(stayTs) {
				delete(ds.toArchive, p)
				toarchive = append(toarchive, p)
			}
		}
		ds.mu.Unlock()

		for _, p := range todel {
			log.Printf("delete %q", p)
			qad.RemoveFile(p)
		}

		for _, p := range toarchive {
			if !strings.HasSuffix(p, ".archive") {
				log.Fatalf("file:%s not end with .archive", p)
			}
			dst := filepath.Join(archiveDir, filepath.Base(strings.TrimSuffix(p, ".archive")))
			if dsize, ok := qad.FileSize(dst); ok {
				ssize, _ := qad.FileSize(p)
				if ssize == dsize {
					log.Printf("%q is already archived", p)
					qad.RemoveFile(p)
				} else {
					log.Printf("cannot archive %q, file exists", p)
				}
				continue
			}
			log.Printf("archive to %q", dst)
			qad.MoveFile(p, dst)
		}
	}
}

func sha(name string) string {
	hh := sha256.New224()
	io.WriteString(hh, name)
	return strings.ToLower(base32.StdEncoding.EncodeToString(hh.Sum(nil)))
}

func minPrefix(files []FileInfo) {
	n := 1
L:
	for ; n < 41; n++ {
		seen := make(map[string]struct{})
		for _, fi := range files {
			x := fi.ID[:n]
			if _, ok := seen[x]; ok {
				continue L
			}
			seen[x] = struct{}{}
		}
		break
	}

	for i := range files {
		files[i].ID = files[i].ID[:n] // change in-place
	}
}

func (ds *Dirs) discoverGarbage() {
	var todel, toarchive []string
	for _, d := range ds.dirs {
		fs.WalkDir(os.DirFS(d), ".", func(p string, _ fs.DirEntry, err error) error {
			qad.Assert(err, "walkdir", d)
			switch filepath.Ext(p) {
			case ".delete":
				p := filepath.Join(d, p)
				todel = append(todel, p)
			case ".archive":
				p := filepath.Join(d, p)
				toarchive = append(toarchive, p)
			}
			return nil
		})
	}
	log.Printf("to delete old %v", todel)
	log.Printf("to archive old %v", toarchive)
	now := time.Now()
	for _, p := range todel {
		ds.toDel[p] = now
	}
	for _, p := range toarchive {
		ds.toArchive[p] = now
	}
}

func (ds *Dirs) refresh() {
	var files []FileInfo
	for _, d := range ds.dirs {
		fs.WalkDir(os.DirFS(d), ".", func(p string, _ fs.DirEntry, err error) error {
			qad.Assert(err, "walkdir", d)
			for _, sfx := range ds.suffixes {
				if strings.HasSuffix(p, sfx) {
					p := filepath.Join(d, p)
					files = append(files, FileInfo{Path: p, ID: sha(p), ModTime: qad.FileModTime(p)})
					break
				}
			}
			return nil
		})
	}

	minPrefix(files)

	// TODO: sort by (parentDir, modtime)
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime.Before(files[j].ModTime) })

	ds.files = files
}

var pathRe = regexp.MustCompile(`^/f/([0-9a-z]+)$`)

func (ds *Dirs) ServeFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	ms := pathRe.FindStringSubmatch(p)
	if len(ms) != 2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	fid := ms[1]

	ds.mu.Lock()
	filename, ok := ds.getFileByID(fid)
	ds.mu.Unlock()
	if !ok {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, filename)
}

var archiveDir string

func main() {
	suffixFlag := flag.String("f", "mp4,mkv", "suffixes supported")
	flag.StringVar(&archiveDir, "a", "", "archive dir")
	portFlag := flag.Int("p", 5555, "port")
	flag.Parse()
	if flag.NArg() == 0 {
		log.Fatal("no dir is specified")
	}

	var suffixes []string
	for _, x := range strings.Split(*suffixFlag, ",") {
		suffixes = append(suffixes, "."+x)
	}

	ds := New(flag.Args(), suffixes)
	go ds.runDeleter()

	http.HandleFunc("/", ds.Index)
	http.HandleFunc("/f/", ds.ServeFile)
	http.HandleFunc("/delete/", ds.DeleteFile)
	http.HandleFunc("/archive/", ds.ArchiveFile)
	http.Handle("/asset/", http.FileServer(http.FS(asset)))
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *portFlag), nil))
}
