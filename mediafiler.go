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

//go:embed asset
var asset embed.FS

var indexTmpl = func() *template.Template {
	index, err := asset.ReadFile("asset/index.html")
	if err != nil {
		log.Fatal(err)
	}
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
	if err != nil {
		log.Fatal("execute", err, bb.String())
	}
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

	fid := filepath.Base(r.URL.Path)
	_, undelete := r.URL.Query()["undelete"]

	filename, ok := ds.getFileByID(fid)
	if !ok {
		http.Error(w, "Invalid id to archive", http.StatusBadRequest)
		return
	}

	if undelete {
		delete(ds.toArchive, filename)
	} else {
		ds.toArchive[filename] = time.Now()
	}
}

func (ds *Dirs) DeleteFile(w http.ResponseWriter, r *http.Request) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	fid := filepath.Base(r.URL.Path)
	_, undelete := r.URL.Query()["undelete"]

	filename, ok := ds.getFileByID(fid)
	if !ok {
		http.Error(w, "Invalid id to delete", http.StatusBadRequest)
		return
	}

	from := filename
	to := filename + ".del"
	if undelete {
		from, to = to, from
	}

	err := os.Rename(from, to)
	if err != nil {
		http.Error(w, "Rename error", http.StatusInternalServerError)
		return
	}

	if undelete {
		delete(ds.toDel, from)
	} else {
		ds.toDel[to] = time.Now()
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
			err := os.Remove(p)
			if err != nil {
				log.Printf("delete %s err:%v", p, err)
			}
		}

		for _, p := range toarchive {
			np := filepath.Join(archiveDir, filepath.Base(p))
			err := os.Rename(p, np)
			if err != nil {
				log.Printf("rename %s -> %s err:%v", p, np, err)
			}
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

func (ds *Dirs) refresh() {
	var files []FileInfo
	for _, d := range ds.dirs {
		fs.WalkDir(os.DirFS(d), ".", func(p string, _ fs.DirEntry, err error) error {
			if err != nil {
				log.Fatal("walkdir", d, err)
			}
			for _, sfx := range ds.suffixes {
				if strings.HasSuffix(p, sfx) {
					p := filepath.Join(d, p)
					st, _ := os.Stat(p)
					files = append(files, FileInfo{Path: p, ID: sha(p), ModTime: st.ModTime()})
					break
				}
			}
			return nil
		})
	}

	minPrefix(files)

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

	dirs := Dirs{
		dirs:      flag.Args(),
		suffixes:  suffixes,
		toDel:     make(map[string]time.Time),
		toArchive: make(map[string]time.Time),
	}
	go dirs.runDeleter()

	http.HandleFunc("/", dirs.Index)
	http.HandleFunc("/f/", dirs.ServeFile)
	http.HandleFunc("/delete/", dirs.DeleteFile)
	http.HandleFunc("/archive/", dirs.ArchiveFile)
	http.Handle("/asset/", http.FileServer(http.FS(asset)))
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *portFlag), nil))
}
