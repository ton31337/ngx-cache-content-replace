package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/spf13/cobra"
)

const (
	NGX_HTTP_CACHE_KEY_LEN  = 16
	NGX_HTTP_CACHE_ETAG_LEN = 128
	NGX_HTTP_CACHE_VARY_LEN = 128
)

var (
	byteOrder     = binary.LittleEndian
	HeaderSize    = int(unsafe.Sizeof(Header{}))
	cacheFile     string
	dataFile      string
	extractToFile string
	dryRun        bool
	rootCmd       = &cobra.Command{
		Use:   "ngx-cache-content-replace",
		Short: "Replace the original cached content in Nginx cache file",
		Run: func(cmd *cobra.Command, args []string) {
		},
	}
	cache_key_head = []byte{'\n', 'K', 'E', 'Y', ':', ' '}
)

// Nginx cache version 5 only is supported
type Header struct {
	Version      uint64
	ValidSec     uint64
	UpdatingSec  uint64
	ErrorSec     uint64
	LastModified uint64
	Date         uint64
	CRC32        uint32
	ValidMsec    uint16
	HeaderStart  uint16
	BodyStart    uint16
	ETagLen      byte
	ETag         [NGX_HTTP_CACHE_ETAG_LEN]byte
	VaryLen      byte
	Vary         [NGX_HTTP_CACHE_VARY_LEN]byte
	Variant      [NGX_HTTP_CACHE_KEY_LEN]byte
}

type FileHandler struct {
	Stat *syscall.Stat_t
	Info fs.FileInfo
	File *os.File
}

type CacheHandler struct {
	Status      []byte
	CacheFile   *FileHandler
	CacheHeader *Header
	CacheKey    []byte
	CacheKeyLen uint16
	Headers     textproto.MIMEHeader
}

func init() {
	rootCmd.Flags().StringVar(&cacheFile, "cache-file", "", "The Nginx's an existing cache file")
	rootCmd.Flags().StringVar(&dataFile, "data-file", "", "The data file to replace an existing cached body")
	rootCmd.Flags().StringVar(&extractToFile, "extract-to-file", "", "Extract cache body to file")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Just print the metadata, ignore real replacement")
	rootCmd.MarkFlagRequired("cache-file")
	rootCmd.Execute()

	if cacheFile == "" {
		os.Exit(1)
	}
}

func headerToBytes(buff io.Writer, header textproto.MIMEHeader) {
	for field, vals := range header {
		for _, subval := range vals {
			io.WriteString(buff, field)
			io.WriteString(buff, ": ")
			buff.Write([]byte(mime.QEncoding.Encode("UTF-8", subval)))
			io.WriteString(buff, "\r\n")
		}
	}
}

func openFile(file string) (*FileHandler, error) {
	fh := &FileHandler{}

	info, err := os.Stat(file)
	if err != nil {
		if os.IsNotExist(err) {
			return fh, err
		}
	}
	stat := info.Sys().(*syscall.Stat_t)

	fh.Stat = stat
	fh.Info = info

	f, err := os.Open(file)
	if err != nil {
		return fh, err
	}

	f.Seek(0, io.SeekStart)

	fh.File = f

	return fh, nil
}

func parseCache() (*CacheHandler, error) {
	ch := &CacheHandler{}

	fCache, err := openFile(cacheFile)
	if err != nil {
		return nil, err
	}

	ch.CacheFile = fCache

	h := new(Header)
	if err := binary.Read(fCache.File, byteOrder, h); err != nil {
		return nil, err
	}

	ch.CacheHeader = h

	// Read cache key (= KEY: 0-http://blabla...)
	cache_key_len := h.HeaderStart - uint16(HeaderSize) - 1
	var cache_key []byte = make([]byte, cache_key_len)
	fCache.File.Seek(int64(HeaderSize), io.SeekStart)
	_, err = fCache.File.Read(cache_key[:])
	if err != nil {
		return nil, err
	}

	ch.CacheKey = cache_key
	ch.CacheKeyLen = cache_key_len

	// Read headers, and status line from the original cache file
	fCache.File.Seek(int64(h.HeaderStart), io.SeekStart)
	htr := bufio.NewReader(io.MultiReader(
		io.LimitReader(fCache.File, int64(h.BodyStart-h.HeaderStart)),
		strings.NewReader("\r\n"),
	))
	status, _ := htr.ReadBytes('\n')
	headers, err := textproto.NewReader(htr).ReadMIMEHeader()
	if err != nil {
		return nil, err
	}

	ch.Status = status
	ch.Headers = headers

	return ch, nil
}

func extractCacheBody() error {
	ch, err := parseCache()
	if err != nil {
		return err
	}
	defer ch.CacheFile.File.Close()
	headers := ch.Headers

	out, err := os.Create(extractToFile)
	if err != nil {
		return err
	}

	fmt.Printf("Content-Type: %s\n", headers.Get("Content-Type"))
	fmt.Printf("Content-Length: %s\n", headers.Get("Content-Length"))

	ch.CacheFile.File.Seek(int64(ch.CacheHeader.BodyStart), io.SeekStart)

	io.Copy(out, ch.CacheFile.File)

	return nil
}

func updateCacheBody() error {
	var tmpCacheFile = fmt.Sprintf("%s.tmp", cacheFile)

	fData, err := openFile(dataFile)
	if err != nil {
		return err
	}
	defer fData.File.Close()

	ch, err := parseCache()
	if err != nil {
		return err
	}
	defer ch.CacheFile.File.Close()
	headers := ch.Headers

	out, err := os.Create(tmpCacheFile)
	if err != nil {
		return err
	}

	if s := headers.Get("Content-Type"); s == "image/png" {
		size := strconv.FormatInt(fData.Info.Size(), 10)
		headers.Set("Content-Type", "image/webp")
		headers.Set("Content-Length", size)
	}

	// Save original data
	ch.CacheFile.File.Seek(int64(ch.CacheHeader.BodyStart), io.SeekStart)
	buff := bytes.NewBuffer(make([]byte, 0, 8192))
	headerToBytes(buff, headers)
	header_length := len(buff.Bytes())

	// Change header/body start positions
	ch.CacheHeader.HeaderStart = uint16(HeaderSize) + uint16(binary.Size(ch.CacheKey)) + 1
	ch.CacheHeader.BodyStart = ch.CacheHeader.HeaderStart + uint16(header_length) + uint16(len(headers)) + 2

	fmt.Printf("Cache key length: %d\n", ch.CacheKeyLen)
	fmt.Printf("Cache key: %s\n", ch.CacheKey[len(cache_key_head):])
	fmt.Printf("Headers length: %d\n", header_length)
	fmt.Printf("Headers start: %d\n", ch.CacheHeader.HeaderStart)
	fmt.Printf("Headers: %s\n", headers)
	fmt.Printf("Body start: %d\n", ch.CacheHeader.BodyStart)

	// Write generic cache file header
	binary.Write(out, byteOrder, ch.CacheHeader)
	binary.Write(out, byteOrder, []byte{'\000', '\000', '\000', '\000'})
	binary.Write(out, byteOrder, ch.CacheKey)
	binary.Write(out, byteOrder, []byte{'\n'})
	binary.Write(out, byteOrder, []byte(ch.Status))

	// Write headers
	binary.Write(out, byteOrder, buff.Bytes())
	binary.Write(out, byteOrder, []byte{'\r', '\n', '\r', '\n'})

	// Write body
	out.Seek(int64(ch.CacheHeader.BodyStart), io.SeekStart)
	io.Copy(out, fData.File)

	if !dryRun {
		out.Sync()

		err = os.Rename(tmpCacheFile, cacheFile)
		if err != nil {
			return err
		}

		err = os.Chmod(cacheFile, fs.FileMode(ch.CacheFile.Info.Mode()))
		if err != nil {
			return err
		}

		err = os.Chown(cacheFile, int(ch.CacheFile.Stat.Uid), int(ch.CacheFile.Stat.Gid))
		if err != nil {
			return err
		}
	} else {
		if err = os.Remove(tmpCacheFile); err != nil {
			return err
		}
	}

	return nil
}

func main() {
	if dataFile != "" {
		if err := updateCacheBody(); err != nil {
			log.Fatal(err)
		}
	} else if extractToFile != "" {
		if err := extractCacheBody(); err != nil {
			log.Fatal(err)
		}
	}
}
