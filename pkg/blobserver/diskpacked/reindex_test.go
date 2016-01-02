/*
Copyright 2014 Google Inc.

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
	"camlistore.org/pkg/blob"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

type blobStat struct {
	id     int
	ref    string
	offset int64
	size   uint32
}

func TestWalkPackBlock(t *testing.T) {
	blockDevice := os.Getenv("CAMLI_DP_DEVICE")
	if blockDevice == "" {
		t.Skip("No loopback device given (CAMLI_DP_DEVICE)")
	} else {
		/*
			cmd := exec.Command("/sbin/losetup", blockDevice, "testdata/sparse-file-2")
			if err := cmd.Run(); err != nil {
				t.Fatalf("mount loop back device %s failed: %s", blockDevice, err)
				return
			}
		*/
		ofArg := fmt.Sprintf("of=%s", blockDevice)
		cmd := exec.Command("dd", "if=testdata/pack-00000.blobs", ofArg, "bs=1", "seek=512")
		cmd.Run()
		cmd = exec.Command("dd", "if=testdata/pack-00001.blobs", ofArg, "bs=1", "seek=730")
		cmd.Run()
		/*
			defer func() {
				cmd := exec.Command("/sbin/losetup", "-d", blockDevice)
				if err := cmd.Run(); err != nil {
					t.Logf("umount loop back device %s fail: %s", blockDevice, err)
					return
				}
			}()
		*/
		s := storage{root: blockDevice}
		s.isRawBlkDev = true
		if fd, err := os.Open(blockDevice); err != nil {
			return
		} else {
			s.fds = append(s.fds, fd)
			want := []blobStat{
				{0, "sha1-f7ff9e8b7bb2e09b70935a5d785e0cc5d9d0abf0", 561, 5},
				{0, "sha1-70c07ec18ef89c5309bbb0937f3a6342411e1fdd", 615, 5},
				{0, "<invalid-blob.Ref>", 669, 7},
				{0, "sha1-70c07ec18ef89c5309bbb0937f3a6342411e1fdd", 725, 5},
				{0, "sha1-fe05bcdcdc4928012781a5f1a2a77cbb5398e106", 779, 3},
				{0, "sha1-ad782ecdac770fc6eb9a62e44f90873fb97fb26b", 831, 3},
				{0, "sha1-b802f384302cb24fbab0a44997e820bf2e8507bb", 883, 5},
			}
			execTestWalkPack(t, s, want)
		}
	}
}

func TestWalkPack(t *testing.T) {
	s := storage{root: "testdata"}
	want := []blobStat{
		{0, "sha1-f7ff9e8b7bb2e09b70935a5d785e0cc5d9d0abf0", 49, 5},
		{0, "sha1-70c07ec18ef89c5309bbb0937f3a6342411e1fdd", 103, 5},
		{0, "<invalid-blob.Ref>", 157, 7},
		{0, "sha1-70c07ec18ef89c5309bbb0937f3a6342411e1fdd", 213, 5},
		{1, "sha1-fe05bcdcdc4928012781a5f1a2a77cbb5398e106", 49, 3},
		{1, "sha1-ad782ecdac770fc6eb9a62e44f90873fb97fb26b", 101, 3},
		{1, "sha1-b802f384302cb24fbab0a44997e820bf2e8507bb", 153, 5},
	}
	execTestWalkPack(t, s, want)
}
func execTestWalkPack(t *testing.T, s storage, want []blobStat) {
	var got []blobStat
	walk := func(packID int, ref blob.Ref, offset int64, size uint32) error {
		t.Log(packID, ref, offset, size)
		got = append(got, blobStat{
			id:     packID,
			ref:    ref.String(),
			offset: offset,
			size:   size,
		})
		return nil
	}

	if err := s.Walk(nil, walk); err != nil {
		t.Fatal(err)
	}

	if len(got) != len(want) {
		t.Errorf("Got len %q want len %q", got, want)
	}
	for i, g := range got {
		w := want[i]
		if g.id != w.id || g.ref != w.ref || g.offset != w.offset || g.size != w.size {
			t.Errorf("%d: got %d, %q, %d, %d want %d, %q, %d, %d", i, g.id, g.ref, g.offset, g.size, w.id, w.ref, w.offset, w.size)
		}
	}
}
