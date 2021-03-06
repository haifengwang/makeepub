package main

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	reComment = regexp.MustCompile("^[ \t]*<!--<[hH]([1-6])[^>]*>([^<]*)</[hH]([1-6])>-->[ \t]*$")
	reHeader  = regexp.MustCompile("^[ \t]*<[hH]([1-6])[^>]*>([^<]*)</[hH]([1-6])>[ \t]*$")
	reBody    = regexp.MustCompile("^[ \t]*<(?i)body(?-i)[^>]*>$")
)

type EpubMaker struct {
	folder VirtualFolder
	book   *Epub
	logger *log.Logger
	cfg    *Config
}

func NewEpubMaker(logger *log.Logger) *EpubMaker {
	return &EpubMaker{logger: logger}
}

func (this *EpubMaker) loadConfig() error {
	rc, e := this.folder.OpenFile("book.ini")
	if e != nil {
		return e
	}
	defer rc.Close()
	this.cfg, e = ParseIni(rc)
	return e
}

func (this *EpubMaker) addFilesToBook() error {
	walk := func(path string) error {
		p := strings.ToLower(path)
		if p == "book.ini" || p == "book.html" {
			return nil
		}

		rc, e := this.folder.OpenFile(path)
		if e != nil {
			return e
		}
		defer rc.Close()
		data, e := ioutil.ReadAll(rc)
		if e != nil {
			return e
		}

		if p == "cover.png" || p == "cover.jpg" || p == "cover.gif" {
			if e := this.book.SetCoverImage(p); e != nil {
				return e
			}
		}
		return this.book.AddFile(path, data)
	}

	return this.folder.Walk(walk)
}

func getChapterHeader(scanner *bufio.Scanner) ([]byte, error) {
	buf := new(bytes.Buffer)

	for scanner.Scan() {
		l := scanner.Bytes()
		buf.Write(l)
		buf.WriteByte('\n')
		if reBody.Match(l) {
			break
		}
	}
	if e := scanner.Err(); e != nil {
		return nil, e
	}

	return removeUtf8Bom(buf.Bytes()), nil
}

func checkNewChapter(re *regexp.Regexp, l []byte) (depth int, title string) {
	if m := re.FindSubmatch(l); m != nil && m[1][0] == m[3][0] {
		depth = int(m[1][0] - '0')
		title = string(m[2])
	}
	return
}

func (this *EpubMaker) splitChapter(header []byte, scanner *bufio.Scanner) error {
	maxDepth := this.cfg.GetInt("/book/depth", 1)
	if maxDepth < 1 || maxDepth > this.book.MaxDepth() {
		this.writeLog("invalid 'depth' value, reset to '1'.")
		maxDepth = 1
	}

	re := reHeader
	if d := strings.ToLower(this.cfg.GetString("/book/separator", "header")); d != "header" {
		if d == "comment" {
			re = reComment
		} else {
			this.writeLog("invalid 'separator' value, use 'header' as default.")
		}
	}

	depth, title, buf := 1, "", new(bytes.Buffer)
	for scanner.Scan() {
		l := scanner.Bytes()
		if nd, nt := checkNewChapter(re, l); nd > 0 && nd <= maxDepth {
			if buf.Len() > 0 {
				buf.WriteString("	</body>\n</html>")
				if e := this.book.AddChapter(title, buf.Bytes(), depth); e != nil {
					return e
				}
				buf.Reset()
			}
			depth, title = nd, nt
			buf.Write(header)
		}

		buf.Write(l)
		buf.WriteByte('\n')
	}
	if e := scanner.Err(); e != nil {
		return e
	}

	if buf.Len() > 0 {
		return this.book.AddChapter(title, buf.Bytes(), depth)
	}

	return nil
}

func (this *EpubMaker) addChaptersToBook() error {
	f, e := this.folder.OpenFile("book.html")
	if e != nil {
		return e
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)

	header, e := getChapterHeader(scanner)
	if e != nil {
		return e
	}

	return this.splitChapter(header, scanner)
}

func (this *EpubMaker) writeLog(msg string) {
	this.logger.Printf("%s: %s\n", this.folder.Name(), msg)
}

func (this *EpubMaker) initBook() (e error) {
	if this.book, e = NewEpub(false); e != nil {
		this.writeLog("failed to create epub book.")
		return e
	}

	s := this.cfg.GetString("/book/id", "")
	this.book.SetId(s)

	s = this.cfg.GetString("/book/name", "")
	if len(s) == 0 {
		this.writeLog("book name is empty.")
	}
	this.book.SetName(s)

	s = this.cfg.GetString("/book/author", "")
	if len(s) == 0 {
		this.writeLog("author name is empty.")
	}
	this.book.SetAuthor(s)

	return nil
}

func (this *EpubMaker) Process(folder VirtualFolder) (e error) {
	this.folder = folder

	if e = this.loadConfig(); e != nil {
		this.writeLog("failed to open configuration file.")
		return e
	}

	if e = this.initBook(); e != nil {
		return e
	}

	if e = this.addFilesToBook(); e != nil {
		this.writeLog("failed to add files to book.")
		return e
	}

	if e = this.addChaptersToBook(); e != nil {
		this.writeLog("failed to add chapters to book.")
		return e
	}

	if e = this.book.Close(); e != nil {
		this.writeLog("failed to close book.")
		return e
	}

	return nil
}

func (this *EpubMaker) SaveTo(outdir string) error {
	s := this.cfg.GetString("/output/path", "")
	if len(s) == 0 {
		this.writeLog("output path is empty, no file will be created.")
		return nil
	}

	if len(outdir) != 0 {
		_, s = filepath.Split(s)
		s = filepath.Join(outdir, s)
	}
	if e := this.book.Save(s); e != nil {
		this.writeLog("failed to create output file.")
		return e
	}

	this.writeLog("output file created at '" + s + "'.")
	return nil
}

func (this *EpubMaker) GetResult() ([]byte, string) {
	name := this.cfg.GetString("/output/path", "")
	if len(name) > 0 {
		_, name = filepath.Split(name)
	} else {
		name = "book.epub"
	}

	return this.book.Buffer(), name
}

func RunMake() {
	var outdir string
	if len(os.Args) > 2 {
		outdir = os.Args[2]
	}

	maker := NewEpubMaker(logger)

	if folder, e := OpenVirtualFolder(os.Args[1]); e != nil {
		logger.Fatalf("%s: failed to open source folder/file.\n", os.Args[1])
	} else if maker.Process(folder) != nil {
		os.Exit(1)
	} else if maker.SaveTo(outdir) != nil {
		os.Exit(1)
	}
}
