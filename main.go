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
	byteOrder  = binary.LittleEndian
	HeaderSize = int(unsafe.Sizeof(Header{}))
	cacheFile  string
	dataFile   string
	dryRun     bool
	rootCmd    = &cobra.Command{
		Use:   "ngx-cache-content-replace",
		Short: "Replace the original cached content in Nginx cache file",
		Run: func(cmd *cobra.Command, args []string) {
		},
	}
	cache_key_head = []byte{'\n', 'K', 'E', 'Y', ':', ' '}
)

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

func init() {
	// TODO: CLI option to take be body of the cacheFile, run cwebp and
	// put generated webp data into the new file as the data (not dataFile).
	rootCmd.Flags().StringVar(&cacheFile, "cache-file", "", "The Nginx's an existing cache file")
	rootCmd.Flags().StringVar(&dataFile, "data-file", "", "The data file to replace an existing cached body")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Just print the metadata, ignore real replacement")
	rootCmd.MarkFlagRequired("cache-file")
	rootCmd.MarkFlagRequired("data-file")
	rootCmd.Execute()

	if cacheFile == "" || dataFile == "" {
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

/*
If we create a new file, but no information in Nginx's rbtree storing a cache, we
will end up with a new file (secondary).
2023/03/07 11:10:59 [debug] 917602#917602: *11 http script copy: "0-http://"
2023/03/07 11:10:59 [debug] 917602#917602: *11 http script var: "cdn.web4testing.com"
2023/03/07 11:10:59 [debug] 917602#917602: *11 http script var: "/wp-content/litespeed/css/f9ea0b165bf4b7cf387288baef2fcc50.css?ver=0"
2023/03/07 11:10:59 [debug] 917602#917602: *11 http cache key: "0-http://cdn.web4testing.com/wp-content/litespeed/css/f9ea0b165bf4b7cf387288baef2fcc50.css?ver=0"
2023/03/07 11:10:59 [debug] 917602#917602: *11 http file cache exists: -5 e:0
2023/03/07 11:10:59 [debug] 917602#917602: *11 cache file: "/var/lib/cache/c/c10efd9e47646b67b9a53c87d7fd333c"
2023/03/07 11:10:59 [debug] 917602#917602: *11 http upstream cache: -5
*/
func updateCacheBody(c *os.File, out *os.File, d *os.File, dInfo fs.FileInfo) error {
	c.Seek(0, io.SeekStart)
	d.Seek(0, io.SeekStart)

	h := new(Header)
	if err := binary.Read(c, byteOrder, h); err != nil {
		return err
	}

	// Read cache key (= KEY: 0-http://blabla...)
	cache_key_len := h.HeaderStart - uint16(HeaderSize) - 1
	var cache_key []byte = make([]byte, cache_key_len)
	c.Seek(int64(HeaderSize), io.SeekStart)
	_, err := c.Read(cache_key[:])
	if err != nil {
		return err
	}

	// Read headers, and status line from the original cache file
	c.Seek(int64(h.HeaderStart), io.SeekStart)
	htr := bufio.NewReader(io.MultiReader(
		io.LimitReader(c, int64(h.BodyStart-h.HeaderStart)),
		strings.NewReader("\r\n"),
	))
	status, _ := htr.ReadBytes('\n')
	headers, err := textproto.NewReader(htr).ReadMIMEHeader()
	if err != nil {
		return err
	}

	if s := headers.Get("Content-Type"); s == "image/png" {
		size := strconv.FormatInt(dInfo.Size(), 10)
		headers.Set("Content-Type", "image/webp")
		headers.Set("Content-Length", size)
	}

	// Save original data
	c.Seek(int64(h.BodyStart), io.SeekStart)
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
	io.Copy(out, d)

	return nil
}

func main() {
	var tmpCacheFile = fmt.Sprintf("%s.tmp", cacheFile)

	cInfo, err := os.Stat(cacheFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("cache file %s\n", cacheFile)
		}
	}
	cStat := cInfo.Sys().(*syscall.Stat_t)

	dInfo, err := os.Stat(dataFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("data file %s\n", dataFile)
		}
	}

	c, err := os.Open(cacheFile)
	if err != nil {
		log.Fatalf("cache file open: %s\n", err)
	}
	defer c.Close()

	d, err := os.Open(dataFile)
	if err != nil {
		log.Fatalf("data file open: %s\n", err)
	}
	defer d.Close()

	tmp, err := os.Create(tmpCacheFile)
	if err != nil {
		log.Fatalf("tmp file create: %s\n", err)
	}

	if err := updateCacheBody(c, tmp, d, dInfo); err != nil {
		log.Fatalf("updateCacheBody(): %s\n", err)
	}

	if !dryRun {
		tmp.Sync()

		err = os.Rename(tmpCacheFile, cacheFile)
		if err != nil {
			log.Fatalf("swap file: %s\n", err)
		}

		err = os.Chmod(cacheFile, fs.FileMode(cStat.Mode))
		if err != nil {
			log.Fatalf("chmod file: %s\n", err)
		}

		err = os.Chown(cacheFile, int(cStat.Uid), int(cStat.Gid))
		if err != nil {
			log.Fatalf("chown file: %s\n", err)
		}
	}
}
