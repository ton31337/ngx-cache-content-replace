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

func extractCacheBody() error {
	fCache, err := openFile(cacheFile)
	if err != nil {
		return err
	}
	defer fCache.File.Close()

	out, err := os.Create(extractToFile)
	if err != nil {
		return err
	}

	h := new(Header)
	if err := binary.Read(fCache.File, byteOrder, h); err != nil {
		return err
	}

	fCache.File.Seek(int64(h.BodyStart), io.SeekStart)

	io.Copy(out, fCache.File)

	return nil
}

func updateCacheBody() error {
	var tmpCacheFile = fmt.Sprintf("%s.tmp", cacheFile)

	fData, err := openFile(dataFile)
	if err != nil {
		return err
	}
	defer fData.File.Close()

	fCache, err := openFile(cacheFile)
	if err != nil {
		return err
	}
	defer fCache.File.Close()

	out, err := os.Create(tmpCacheFile)
	if err != nil {
		return err
	}

	h := new(Header)
	if err := binary.Read(fCache.File, byteOrder, h); err != nil {
		return err
	}

	// Read cache key (= KEY: 0-http://blabla...)
	cache_key_len := h.HeaderStart - uint16(HeaderSize) - 1
	var cache_key []byte = make([]byte, cache_key_len)
	fCache.File.Seek(int64(HeaderSize), io.SeekStart)
	_, err = fCache.File.Read(cache_key[:])
	if err != nil {
		return err
	}

	// Read headers, and status line from the original cache file
	fCache.File.Seek(int64(h.HeaderStart), io.SeekStart)
	htr := bufio.NewReader(io.MultiReader(
		io.LimitReader(fCache.File, int64(h.BodyStart-h.HeaderStart)),
		strings.NewReader("\r\n"),
	))
	status, _ := htr.ReadBytes('\n')
	headers, err := textproto.NewReader(htr).ReadMIMEHeader()
	if err != nil {
		return err
	}

	if s := headers.Get("Content-Type"); s == "image/png" {
		size := strconv.FormatInt(fData.Info.Size(), 10)
		headers.Set("Content-Type", "image/webp")
		headers.Set("Content-Length", size)
	}

	// Save original data
	fCache.File.Seek(int64(h.BodyStart), io.SeekStart)
	buff := bytes.NewBuffer(make([]byte, 0, 8192))
	headerToBytes(buff, headers)
	header_length := len(buff.Bytes())

	// Change header/body start positions
	h.HeaderStart = uint16(HeaderSize) + uint16(binary.Size(cache_key)) + 1
	h.BodyStart = h.HeaderStart + uint16(header_length) + uint16(len(headers)) + 2

	fmt.Printf("Cache key length: %d\n", cache_key_len)
	fmt.Printf("Cache key: %s\n", cache_key[len(cache_key_head):])
	fmt.Printf("Headers length: %d\n", header_length)
	fmt.Printf("Headers start: %d\n", h.HeaderStart)
	fmt.Printf("Headers: %s\n", headers)
	fmt.Printf("Body start: %d\n", h.BodyStart)

	// Write generic cache file header
	binary.Write(out, byteOrder, h)
	binary.Write(out, byteOrder, []byte{'\000', '\000', '\000', '\000'})
	binary.Write(out, byteOrder, cache_key)
	binary.Write(out, byteOrder, []byte{'\n'})
	binary.Write(out, byteOrder, []byte(status))

	// Write headers
	binary.Write(out, byteOrder, buff.Bytes())
	binary.Write(out, byteOrder, []byte{'\r', '\n', '\r', '\n'})

	// Write body
	out.Seek(int64(h.BodyStart), io.SeekStart)
	io.Copy(out, fData.File)

	if !dryRun {
		out.Sync()

		err = os.Rename(tmpCacheFile, cacheFile)
		if err != nil {
			return err
		}

		err = os.Chmod(cacheFile, fs.FileMode(fCache.Info.Mode()))
		if err != nil {
			return err
		}

		err = os.Chown(cacheFile, int(fCache.Stat.Uid), int(fCache.Stat.Gid))
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
