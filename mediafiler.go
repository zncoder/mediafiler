package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base32"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FileInfo struct {
	Path string
	ID   string
}

type Dirs struct {
	dirs     []string
	suffixes []string

	mu    sync.Mutex
	files []FileInfo
	toDel map[string]time.Time // path -> time deleted
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

	var bb bytes.Buffer
	err := indexTmpl.Execute(&bb, &ds.files)
	if err != nil {
		log.Fatal("execute", err, bb.String())
	}
	w.Write(bb.Bytes())
}

func (ds *Dirs) DeleteFile(w http.ResponseWriter, r *http.Request) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	id := filepath.Base(r.URL.Path)
	_, undelete := r.URL.Query()["undelete"]

	var filename string
	for _, fi := range ds.files {
		if fi.ID == id {
			filename = fi.Path
			break
		}
	}

	if filename == "" {
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

		var todel []string
		ds.mu.Lock()
		for p, ts := range ds.toDel {
			if ts.Before(stayTs) {
				delete(ds.toDel, p)
				todel = append(todel, p)
			}
		}
		ds.mu.Unlock()

		for _, p := range todel {
			err := os.Remove(p)
			if err != nil {
				log.Printf("delete %s err:%v", p, err)
			}
		}
	}
}

func sha(name string) string {
	b := sha256.Sum224([]byte(name))
	return base32.StdEncoding.EncodeToString(b[:])[:16]
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
					files = append(files, FileInfo{Path: p, ID: sha(p)})
					break
				}
			}
			return nil
		})
	}

	sort.Slice(files, func(i, j int) bool {
		di, fi := filepath.Split(files[i].Path)
		dj, fj := filepath.Split(files[j].Path)
		if fi == fj {
			return di < dj
		}
		return fi < fj
	})

	ds.files = files
}

var pathRe = regexp.MustCompile(`^/f/([0-9]+)$`)

func (ds *Dirs) ServeFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	ms := pathRe.FindStringSubmatch(p)
	if len(ms) != 2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	fid, _ := strconv.Atoi(ms[1])

	ds.mu.Lock()
	filename := ds.files[fid].Path
	ds.mu.Unlock()
	if filename == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, filename)
}

func main() {
	suffixFlag := flag.String("f", "mp4,mkv", "suffixes supported")
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
		dirs:     flag.Args(),
		suffixes: suffixes,
		toDel:    make(map[string]time.Time),
	}
	go dirs.runDeleter()

	http.HandleFunc("/", dirs.Index)
	http.HandleFunc("/f/", dirs.ServeFile)
	http.HandleFunc("/delete/", dirs.DeleteFile)
	http.Handle("/asset/", http.FileServer(http.FS(asset)))
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *portFlag), nil))
}
