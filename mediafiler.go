package main

import (
	"bytes"
	"embed"
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
)

type FilePath struct {
	Name string
	Path string
}

type Files struct {
	Files []FilePath
}

type Dirs struct {
	dirs     []string
	suffixes []string

	mu sync.Mutex
	Files
}

//go:embed asset
var asset embed.FS

var indexTmpl = func() *template.Template {
	index, err := asset.ReadFile("asset/index.html")
	if err != nil {
		log.Fatal(err)
	}
	return template.Must(template.New("index").Parse(string(index)))
}()

func (ds *Dirs) Index(w http.ResponseWriter, r *http.Request) {
	ds.refresh()

	var bb bytes.Buffer
	err := indexTmpl.Execute(&bb, &ds.Files)
	if err != nil {
		log.Fatal("execute", err, bb.String())
	}
	w.Write(bb.Bytes())
}

func (ds *Dirs) refresh() {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	var files []FilePath
	for _, d := range ds.dirs {
		fs.WalkDir(os.DirFS(d), ".", func(p string, _ fs.DirEntry, err error) error {
			if err != nil {
				log.Fatal("walkdir", d, err)
			}
			for _, sfx := range ds.suffixes {
				if strings.HasSuffix(p, sfx) {
					files = append(files, FilePath{Name: p, Path: filepath.Join(d, p)})
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

	ds.Files.Files = files
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
	filename := ds.Files.Files[fid].Path
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
	}

	http.HandleFunc("/", dirs.Index)
	http.HandleFunc("/f/", dirs.ServeFile)
	http.Handle("/asset/", http.FileServer(http.FS(asset)))
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *portFlag), nil))
}
