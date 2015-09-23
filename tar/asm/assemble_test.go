package asm

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"fmt"
	"hash/crc64"
	"io"
	"io/ioutil"
	"os"
	"testing"

	"github.com/vbatts/tar-split/archive/tar"
	"github.com/vbatts/tar-split/tar/common"
	"github.com/vbatts/tar-split/tar/storage"
)

func TestISO8859(t *testing.T) {
	fh, err := os.Open("./testdata/iso-8859.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	gzRdr, err := gzip.NewReader(fh)
	if err != nil {
		t.Fatal(err)
	}
	defer gzRdr.Close()
	tr := tar.NewReader(gzRdr)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err != io.EOF {
				t.Error(err)
			}
			break
		}
		fmt.Println(hdr.Name)
		if !common.IsValidUtf8String(hdr.Name) {
			fmt.Println([]byte(hdr.Name))
		}
	}
}

var entries = []struct {
	Entry storage.Entry
	Body  []byte
}{
	{
		Entry: storage.Entry{
			Type:    storage.FileType,
			Name:    "./hurr.txt",
			Payload: []byte{2, 116, 164, 177, 171, 236, 107, 78},
			Size:    20,
		},
		Body: []byte("imma hurr til I derp"),
	},
	{
		Entry: storage.Entry{
			Type:    storage.FileType,
			Name:    "./ermahgerd.txt",
			Payload: []byte{126, 72, 89, 239, 230, 252, 160, 187},
			Size:    26,
		},
		Body: []byte("café con leche, por favor"),
	},
	{
		Entry: storage.Entry{
			Type:    storage.FileType,
			NameRaw: []byte{0x66, 0x69, 0x6c, 0x65, 0x2d, 0xe4}, // this is invalid UTF-8. Just checking the round trip.
			Payload: []byte{126, 72, 89, 239, 230, 252, 160, 187},
			Size:    26,
		},
		Body: []byte("café con leche, por favor"),
	},
}
var entriesMangled = []struct {
	Entry storage.Entry
	Body  []byte
}{
	{
		Entry: storage.Entry{
			Type:    storage.FileType,
			Name:    "./hurr.txt",
			Payload: []byte{3, 116, 164, 177, 171, 236, 107, 78},
			Size:    20,
		},
		// switch
		Body: []byte("imma derp til I hurr"),
	},
	{
		Entry: storage.Entry{
			Type:    storage.FileType,
			Name:    "./ermahgerd.txt",
			Payload: []byte{127, 72, 89, 239, 230, 252, 160, 187},
			Size:    26,
		},
		// san not con
		Body: []byte("café sans leche, por favor"),
	},
	{
		Entry: storage.Entry{
			Type:    storage.FileType,
			NameRaw: []byte{0x66, 0x69, 0x6c, 0x65, 0x2d, 0xe4},
			Payload: []byte{127, 72, 89, 239, 230, 252, 160, 187},
			Size:    26,
		},
		Body: []byte("café con leche, por favor"),
	},
}

func TestTarStreamMangledGetterPutter(t *testing.T) {
	fgp := storage.NewBufferFileGetPutter()

	// first lets prep a GetPutter and Packer
	for i := range entries {
		if entries[i].Entry.Type == storage.FileType {
			j, csum, err := fgp.Put(entries[i].Entry.GetName(), bytes.NewBuffer(entries[i].Body))
			if err != nil {
				t.Error(err)
			}
			if j != entries[i].Entry.Size {
				t.Errorf("size %q: expected %d; got %d",
					entries[i].Entry.GetName(),
					entries[i].Entry.Size,
					j)
			}
			if !bytes.Equal(csum, entries[i].Entry.Payload) {
				t.Errorf("checksum %q: expected %v; got %v",
					entries[i].Entry.GetName(),
					entries[i].Entry.Payload,
					csum)
			}
		}
	}

	for _, e := range entriesMangled {
		if e.Entry.Type == storage.FileType {
			rdr, err := fgp.Get(e.Entry.GetName())
			if err != nil {
				t.Error(err)
			}
			c := crc64.New(storage.CRCTable)
			i, err := io.Copy(c, rdr)
			if err != nil {
				t.Fatal(err)
			}
			rdr.Close()

			csum := c.Sum(nil)
			if bytes.Equal(csum, e.Entry.Payload) {
				t.Errorf("wrote %d bytes. checksum for %q should not have matched! %v",
					i,
					e.Entry.GetName(),
					csum)
			}
		}
	}
}

func TestTarStream(t *testing.T) {
	testCases := []struct {
		path            string
		expectedSHA1Sum string
		expectedSize    int64
	}{
		{"./testdata/t.tar.gz", "1eb237ff69bca6e22789ecb05b45d35ca307adbd", 10240},
		{"./testdata/longlink.tar.gz", "d9f6babe107b7247953dff6b5b5ae31a3a880add", 20480},
		{"./testdata/fatlonglink.tar.gz", "8537f03f89aeef537382f8b0bb065d93e03b0be8", 26234880},
		{"./testdata/iso-8859.tar.gz", "ddafa51cb03c74ec117ab366ee2240d13bba1ec3", 10240},
	}

	for _, tc := range testCases {
		fh, err := os.Open(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		defer fh.Close()
		gzRdr, err := gzip.NewReader(fh)
		if err != nil {
			t.Fatal(err)
		}
		defer gzRdr.Close()

		// Setup where we'll store the metadata
		w := bytes.NewBuffer([]byte{})
		sp := storage.NewJSONPacker(w)
		fgp := storage.NewBufferFileGetPutter()

		// wrap the disassembly stream
		tarStream, err := NewInputTarStream(gzRdr, sp, fgp)
		if err != nil {
			t.Fatal(err)
		}

		// get a sum of the stream after it has passed through to ensure it's the same.
		h0 := sha1.New()
		tRdr0 := io.TeeReader(tarStream, h0)

		// read it all to the bit bucket
		i, err := io.Copy(ioutil.Discard, tRdr0)
		if err != nil {
			t.Fatal(err)
		}

		if i != tc.expectedSize {
			t.Errorf("size of tar: expected %d; got %d", tc.expectedSize, i)
		}
		if fmt.Sprintf("%x", h0.Sum(nil)) != tc.expectedSHA1Sum {
			t.Fatalf("checksum of tar: expected %s; got %x", tc.expectedSHA1Sum, h0.Sum(nil))
		}

		//t.Logf("%s", w.String()) // if we fail, then show the packed info

		// If we've made it this far, then we'll turn it around and create a tar
		// stream from the packed metadata and buffered file contents.
		r := bytes.NewBuffer(w.Bytes())
		sup := storage.NewJSONUnpacker(r)
		// and reuse the fgp that we Put the payloads to.

		rc := NewOutputTarStream(fgp, sup)
		h1 := sha1.New()
		i, err = io.Copy(h1, rc)
		if err != nil {
			t.Fatal(err)
		}

		if i != tc.expectedSize {
			t.Errorf("size of output tar: expected %d; got %d", tc.expectedSize, i)
		}
		if fmt.Sprintf("%x", h1.Sum(nil)) != tc.expectedSHA1Sum {
			t.Fatalf("checksum of output tar: expected %s; got %x", tc.expectedSHA1Sum, h1.Sum(nil))
		}
	}
}
