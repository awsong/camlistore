/*
Copyright 2013 The Camlistore Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

/*
In order to run tests against block device, you should set environment
variable CAMLI_DP_DEVICE, and make sure the user running the tests has
write permission to the device.

If there is no block device available, you can also setup loopback by:
"losetup /dev/loopX testdata/sparse.img"
and provide /dev/loopX to CAMLI_DP_DEVICE, the sparse.img file can be
generated like this:
"dd if=/dev/zero of=testdata/sparse.img bs=4M count=0 seek=1000"
*/

package diskpacked

import (
	"bufio"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/env"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/test"
	"go4.org/jsonconfig"
)

func newTempDiskpacked(t *testing.T) (sto blobserver.Storage, cleanup func()) {
	return newTempDiskpackedWithIndex(t, jsonconfig.Obj{}, "")
}

func newTempDiskpackedMemory(t *testing.T) (sto blobserver.Storage, cleanup func()) {
	return newTempDiskpackedWithIndex(t, jsonconfig.Obj{
		"type": "memory",
	}, "")
}

func newTempDiskpackedBlock(t *testing.T) (sto blobserver.Storage, cleanup func()) {
	blockDevice := os.Getenv("CAMLI_DP_DEVICE")
	if blockDevice == "" {
		t.Skip("No loopback device given (CAMLI_DP_DEVICE)")
		return nil, nil
	} else {
		/*
			var out bytes.Buffer
			cmd := exec.Command("cp", "testdata/sparse.img", "testdata/tmp.img")
			cmd.Stderr = &out
			if err := cmd.Run(); err != nil {
				t.Fatalf("cp failed: %s", out.String())
				return
			}
			cmd = exec.Command("/sbin/losetup", blockDevice, "testdata/tmp.img")
			cmd.Stderr = &out
			if err := cmd.Run(); err != nil {
				t.Fatalf("mounting loopback device %s failed: %s", blockDevice, out.String())
				return
			}
			defer func() {
				cmd = exec.Command("/sbin/losetup", "-d", blockDevice)
				cmd.Run()
				cmd = exec.Command("rm", "-rf", "testdata/tmp.img")
				cmd.Run()
			}()
		*/
		return newTempDiskpackedWithIndex(t, jsonconfig.Obj{
			"type": "memory",
		}, blockDevice)
	}
}

func newTempDiskpackedWithIndex(t *testing.T, indexConf jsonconfig.Obj, dir string) (sto blobserver.Storage, cleanup func()) {
	restoreLogging := test.TLog(t)
	var removeDir string
	if dir == "" {
		var err error
		dir, err = ioutil.TempDir("", "diskpacked-test")
		if err != nil {
			t.Fatal(err)
		}
		removeDir = dir
	} else {
		removeDir = ""
	}

	t.Logf("diskpacked test dir is %q", dir)
	s, err := newStorage(dir, 1<<20, indexConf)
	if err != nil {
		t.Fatalf("newStorage: %v", err)
	}
	return s, func() {
		s.Close()
		if env.IsDebug() {
			t.Logf("CAMLI_DEBUG set, skipping cleanup of dir %q", dir)
		} else {
			os.RemoveAll(removeDir)
		}
		restoreLogging()
	}
}

func TestDiskpacked(t *testing.T) {
	storagetest.Test(t, newTempDiskpacked)
}

func TestDiskpackedAltIndex(t *testing.T) {
	storagetest.Test(t, newTempDiskpackedMemory)
}

func TestDiskpackedBlock(t *testing.T) {
	storagetest.Test(t, newTempDiskpackedBlock)
}

func TestDoubleReceiveBlock(t *testing.T) {
	sto, cleanup := newTempDiskpackedBlock(t)
	defer cleanup()

	const blobSize = 5 << 10
	b := &test.Blob{Contents: strings.Repeat("a", blobSize)}
	br := b.BlobRef()

	_, err := blobserver.Receive(sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if sto.(*storage).sb.Pos < blobSize {
		t.Fatalf("size = %d; want at least %d", sto.(*storage).sb.Pos, blobSize)
	}

	Pos := sto.(*storage).sb.Pos
	_, err = blobserver.Receive(sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if sto.(*storage).sb.Pos > Pos {
		t.Fatalf("size = %d; appeared to double-write. blobSize %d", sto.(*storage).sb.Pos, blobSize)
	}

}

func TestDoubleReceive(t *testing.T) {
	sto, cleanup := newTempDiskpacked(t)
	defer cleanup()

	size := func(n int) int64 {
		path := sto.(*storage).filename(n)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}

	const blobSize = 5 << 10
	b := &test.Blob{Contents: strings.Repeat("a", blobSize)}
	br := b.BlobRef()

	_, err := blobserver.Receive(sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if size(0) < blobSize {
		t.Fatalf("size = %d; want at least %d", size(0), blobSize)
	}

	sto.(*storage).nextPack()

	_, err = blobserver.Receive(sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	sizePostDup := size(1)
	if sizePostDup >= blobSize {
		t.Fatalf("size(pack1) = %d; appeared to double-write.", sizePostDup)
	}

	os.Remove(sto.(*storage).filename(0))
	_, err = blobserver.Receive(sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	sizePostDelete := size(1)
	if sizePostDelete < blobSize {
		t.Fatalf("after packfile delete + reupload, not big enough. want size of a blob")
	}
}

func TestDeleteBlock(t *testing.T) {
	sto, cleanup := newTempDiskpackedBlock(t)
	defer cleanup()

	execDelete(t, sto)
}

func TestDelete(t *testing.T) {
	sto, cleanup := newTempDiskpacked(t)
	defer cleanup()

	execDelete(t, sto)
}

func execDelete(t *testing.T, sto blobserver.Storage) {
	var (
		A = &test.Blob{Contents: "some small blob"}
		B = &test.Blob{Contents: strings.Repeat("some middle blob", 100)}
		C = &test.Blob{Contents: strings.Repeat("A 8192 bytes length largish blob", 8192/32)}
	)

	type step func() error

	stepAdd := func(tb *test.Blob) step { // add the blob
		return func() error {
			sb, err := sto.ReceiveBlob(tb.BlobRef(), tb.Reader())
			if err != nil {
				return fmt.Errorf("ReceiveBlob of %s: %v", sb, err)
			}
			if sb != tb.SizedRef() {
				return fmt.Errorf("Received %v; want %v", sb, tb.SizedRef())
			}
			return nil
		}
	}

	stepCheck := func(want ...*test.Blob) step { // check the blob
		wantRefs := make([]blob.SizedRef, len(want))
		for i, tb := range want {
			wantRefs[i] = tb.SizedRef()
		}
		return func() error {
			if err := storagetest.CheckEnumerate(sto, wantRefs); err != nil {
				return err
			}
			return nil
		}
	}

	stepDelete := func(tb *test.Blob) step {
		return func() error {
			if err := sto.RemoveBlobs([]blob.Ref{tb.BlobRef()}); err != nil {
				return fmt.Errorf("RemoveBlob(%s): %v", tb.BlobRef(), err)
			}
			return nil
		}
	}

	var deleteTests = [][]step{
		[]step{
			stepAdd(A),
			stepDelete(A),
			stepCheck(),
			stepAdd(B),
			stepCheck(B),
			stepDelete(B),
			stepCheck(),
			stepAdd(C),
			stepCheck(C),
			stepAdd(A),
			stepCheck(A, C),
			stepDelete(A),
			stepDelete(C),
			stepCheck(),
		},
		[]step{
			stepAdd(A),
			stepAdd(B),
			stepAdd(C),
			stepCheck(A, B, C),
			stepDelete(C),
			stepCheck(A, B),
		},
	}
	for i, steps := range deleteTests {
		for j, s := range steps {
			if err := s(); err != nil {
				t.Errorf("error at test %d, step %d: %v", i+1, j+1, err)
			}
		}
	}
}

var dummyErr = errors.New("dummy fail")

func TestDoubleReceiveFailingIndex(t *testing.T) {
	sto, cleanup := newTempDiskpacked(t)
	defer cleanup()

	sto.(*storage).index = &failingIndex{KeyValue: sto.(*storage).index}

	size := func(n int) int64 {
		path := sto.(*storage).filename(n)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}

	const blobSize = 5 << 10
	b := &test.Blob{Contents: strings.Repeat("a", blobSize)}
	br := b.BlobRef()

	_, err := blobserver.Receive(sto, br, b.Reader())
	if err != nil {
		if err != dummyErr {
			t.Fatal(err)
		}
		t.Logf("dummy fail")
	}
	if size(0) >= blobSize {
		t.Fatalf("size = %d; want zero (at most %d)", size(0), blobSize-1)
	}

	_, err = blobserver.Receive(sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if size(0) < blobSize {
		t.Fatalf("size = %d; want at least %d", size(0), blobSize)
	}
}

type failingIndex struct {
	sorted.KeyValue
	setCount int
}

func (idx *failingIndex) Set(key string, value string) error {
	idx.setCount++
	if idx.setCount == 1 { // fail the first time
		return dummyErr
	}
	return idx.KeyValue.Set(key, value)
}

func TestReadHeader(t *testing.T) {
	tests := []struct {
		in           string
		wantConsumed int
		wantDigest   string
		wantSize     uint32
		wantErr      bool
	}{
		{"[foo-123 234]", 13, "foo-123", 234, false},

		// Too short:
		{in: "", wantErr: true},
		{in: "[", wantErr: true},
		{in: "[]", wantErr: true},
		// Missing brackets:
		{in: "[foo-123 234", wantErr: true},
		{in: "foo-123 234]", wantErr: true},
		// non-number in size:
		{in: "[foo-123 234x]", wantErr: true},
		// No spce:
		{in: "[foo-abcd1234]", wantErr: true},
	}
	for _, tt := range tests {
		consumed, digest, size, err := readHeader(bufio.NewReader(strings.NewReader(tt.in)))
		if tt.wantErr {
			if err == nil {
				t.Errorf("readHeader(%q) = %d, %q, %v with nil error; but wanted an error",
					tt.in, consumed, digest, size)
			}
		} else if consumed != tt.wantConsumed ||
			string(digest) != tt.wantDigest ||
			size != tt.wantSize ||
			err != nil {
			t.Errorf("readHeader(%q) = %d, %q, %v, %v; want %d, %q, %v, nil",
				tt.in,
				consumed, digest, size, err,
				tt.wantConsumed, tt.wantDigest, tt.wantSize)
		}
	}
}
