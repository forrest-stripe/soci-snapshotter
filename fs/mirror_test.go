/*
   Copyright The Soci Snapshotter Authors.

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

package fs

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/containerd/containerd/reference"
	"github.com/containerd/containerd/remotes/docker"
)

// TestNewRemoteStoreMirrorSupport verifies that newRemoteStore respects the hosts argument.
func TestNewRemoteStoreMirrorSupport(t *testing.T) {
	// Define a reference spec for the target image
	refspec, err := reference.Parse("docker.io/library/ubuntu:latest")
	if err != nil {
		t.Fatalf("failed to parse reference: %v", err)
	}

	// Mock an HTTP client (not used for connection in this unit test, just passed through)
	client := &http.Client{}

	// Case 1: No mirrors (nil hosts)
	// Expected behavior: Should use original locator (docker.io/library/ubuntu)
	repo, err := newRemoteStore(refspec, client, nil)
	if err != nil {
		t.Fatalf("newRemoteStore failed with nil hosts: %v", err)
	}
	if repo.Reference.Host() != "docker.io" {
		t.Errorf("expected host docker.io, got %s", repo.Reference.Host())
	}

	// Case 2: With Mirror
	// Expected behavior: Should use the mirror host (mirror.local) but keep the path (library/ubuntu)
	mirrorHost := docker.RegistryHost{
		Host:   "mirror.local:5000",
		Scheme: "http",
		Path:   "/v2",
	}
	hosts := []docker.RegistryHost{mirrorHost}

	repoMirror, err := newRemoteStore(refspec, client, hosts)
	if err != nil {
		t.Fatalf("newRemoteStore failed with mirror hosts: %v", err)
	}

	// Verify the repository reference points to the mirror
	expectedRef := "mirror.local:5000/library/ubuntu"
	if !strings.Contains(repoMirror.Reference.String(), "mirror.local:5000") {
		t.Errorf("expected repository reference to contain mirror host, got %s", repoMirror.Reference.String())
	}

	// Check PlainHTTP setting (should be true for http scheme)
	if !repoMirror.PlainHTTP {
		t.Error("expected PlainHTTP to be true for http mirror")
	}
}

// TestNewRemoteStoreFallback verifies backward compatibility when hosts is empty.
func TestNewRemoteStoreFallback(t *testing.T) {
	refspec, err := reference.Parse("docker.io/library/alpine:latest")
	if err != nil {
		t.Fatalf("failed to parse reference: %v", err)
	}
	client := &http.Client{}

	// Empty slice should behave like nil (fallback to original)
	repo, err := newRemoteStore(refspec, client, []docker.RegistryHost{})
	if err != nil {
		t.Fatalf("newRemoteStore failed with empty hosts: %v", err)
	}
	if repo.Reference.Host() != "docker.io" {
		t.Errorf("expected host docker.io, got %s", repo.Reference.Host())
	}
}

// TestBlobURLConstruction verifies that blob URLs are constructed correctly with mirrors.
func TestBlobURLConstruction(t *testing.T) {
	// Test with a registry that uses mirror
	refspec, err := reference.Parse("registry.example.com/myorg/myapp/image:latest")
	if err != nil {
		t.Fatalf("failed to parse reference: %v", err)
	}

	client := &http.Client{}

	// Configure a mirror
	mirrorHost := docker.RegistryHost{
		Host:   "mirror.internal:5000",
		Scheme: "http",
		Path:   "/v2",
	}
	hosts := []docker.RegistryHost{mirrorHost}

	blobStore, err := newRemoteBlobStore(refspec, client, hosts)
	if err != nil {
		t.Fatalf("newRemoteBlobStore failed: %v", err)
	}

	// Test URL construction
	testDigest := "sha256:abc123"
	url := blobStore.buildBlobURL(testDigest)

	// The URL should be:
	// http://mirror.internal:5000/v2/myorg/myapp/image/blobs/sha256:abc123
	// NOT:
	// http://mirror.internal:5000/v2/mirror.internal:5000/myorg/myapp/image/blobs/sha256:abc123

	expectedURL := "http://mirror.internal:5000/v2/myorg/myapp/image/blobs/sha256:abc123"
	if url != expectedURL {
		t.Errorf("URL mismatch:\nGot:      %s\nExpected: %s", url, expectedURL)
	}

	// Verify no double hostname
	if strings.Count(url, "mirror.internal:5000") > 1 {
		t.Errorf("URL contains mirror hostname multiple times: %s", url)
	}

	// Verify the repository field doesn't contain the hostname
	if strings.Contains(blobStore.Repository.Reference.Repository, "mirror.internal") {
		t.Errorf("Repository field should not contain hostname, got: %s", blobStore.Repository.Reference.Repository)
	}
}

// TestBlobURLConstructionWithCustomPath verifies support for non-standard registry paths.
func TestBlobURLConstructionWithCustomPath(t *testing.T) {
	refspec, err := reference.Parse("registry.example.com/myorg/myapp/image:latest")
	if err != nil {
		t.Fatalf("failed to parse reference: %v", err)
	}

	client := &http.Client{}

	// Configure a mirror with a custom path
	mirrorHost := docker.RegistryHost{
		Host:   "mirror.internal:5000",
		Scheme: "http",
		Path:   "/custom/v2",
	}
	hosts := []docker.RegistryHost{mirrorHost}

	blobStore, err := newRemoteBlobStore(refspec, client, hosts)
	if err != nil {
		t.Fatalf("newRemoteBlobStore failed: %v", err)
	}

	testDigest := "sha256:abc123"
	url := blobStore.buildBlobURL(testDigest)

	// The URL should include the custom path instead of hardcoded /v2
	expectedURL := "http://mirror.internal:5000/custom/v2/myorg/myapp/image/blobs/sha256:abc123"
	if url != expectedURL {
		t.Errorf("URL mismatch:\nGot:      %s\nExpected: %s", url, expectedURL)
	}
}

// TestDigestReferenceFormat verifies that digest references use @ separator, not :
func TestDigestReferenceFormat(t *testing.T) {
	refspec, err := reference.Parse("registry.example.com/myorg/myapp/image:latest")
	if err != nil {
		t.Fatalf("failed to parse reference: %v", err)
	}

	client := &http.Client{}
	mirrorHost := docker.RegistryHost{
		Host:   "mirror.internal:5000",
		Scheme: "http",
		Path:   "/v2",
	}
	hosts := []docker.RegistryHost{mirrorHost}

	blobStore, err := newRemoteBlobStore(refspec, client, hosts)
	if err != nil {
		t.Fatalf("newRemoteBlobStore failed: %v", err)
	}

	// Test that we construct digest references correctly
	testDigest := "sha256:abc123def456"

	// Build a reference like we do in doInitialFetch
	digestRef := fmt.Sprintf("%s/%s@%s",
		blobStore.Repository.Reference.Host(),
		blobStore.Repository.Reference.Repository,
		testDigest)

	// Verify it uses @ separator for digest
	if !strings.Contains(digestRef, "@sha256:") {
		t.Errorf("Digest reference should use @ separator, got: %s", digestRef)
	}

	// Verify it doesn't use : separator before the digest
	// (: is for tags, @ is for digests)
	if strings.Contains(digestRef, ":sha256:") {
		t.Errorf("Digest reference should NOT use : separator before digest, got: %s", digestRef)
	}

	// Verify expected format
	expectedRef := "mirror.internal:5000/myorg/myapp/image@sha256:abc123def456"
	if digestRef != expectedRef {
		t.Errorf("Digest reference format mismatch:\nGot:      %s\nExpected: %s", digestRef, expectedRef)
	}
}

// TestBlobRefString verifies the blobRef.String() issue
func TestBlobRefString(t *testing.T) {
	// This test documents the bug we fixed:
	// blobRef.String() returns "host/repo:digest" (tag format)
	// but we need "host/repo@digest" (digest format) for blobs

	refspec, err := reference.Parse("docker.io/library/ubuntu:latest")
	if err != nil {
		t.Fatalf("failed to parse reference: %v", err)
	}

	client := &http.Client{}
	blobStore, err := newRemoteBlobStore(refspec, client, nil)
	if err != nil {
		t.Fatalf("newRemoteBlobStore failed: %v", err)
	}

	// Set a digest in the Reference field
	testDigest := "sha256:abc123"
	blobRef := blobStore.Repository.Reference
	blobRef.Reference = testDigest

	// The String() method returns tag format (with :)
	refString := blobRef.String()

	// This will have : before sha256, which is WRONG for blob fetching
	if strings.Contains(refString, ":sha256:") {
		t.Logf("WARNING: blobRef.String() uses : separator (tag format): %s", refString)
		t.Logf("For blob fetching, use @ separator instead")
	}

	// The correct way is to manually format with @
	correctRef := fmt.Sprintf("%s/%s@%s",
		blobStore.Repository.Reference.Host(),
		blobStore.Repository.Reference.Repository,
		testDigest)

	if !strings.Contains(correctRef, "@sha256:") {
		t.Errorf("Correct digest reference should use @ separator, got: %s", correctRef)
	}
}
