package server

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// bundleStore lays out per-job files on disk under the server's data dir:
//
//	<data>/jobs/<id>/bundle/      uploaded source files (the job bundle)
//	<data>/jobs/<id>/artifacts/   outputs uploaded by the agent on completion
//
// Bundles and artifacts move over the tailnet as gzip-compressed tarballs.
type bundleStore struct {
	root string // <data>/jobs
}

func newBundleStore(dataDir string) (*bundleStore, error) {
	root := filepath.Join(dataDir, "jobs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &bundleStore{root: root}, nil
}

func (b *bundleStore) bundleDir(jobID string) string {
	return filepath.Join(b.root, jobID, "bundle")
}

func (b *bundleStore) artifactDir(jobID string) string {
	return filepath.Join(b.root, jobID, "artifacts")
}

// saveBundleFile writes a single uploaded source file into the job bundle.
func (b *bundleStore) saveBundleFile(jobID, filename string, r io.Reader) error {
	dir := b.bundleDir(jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	clean := filepath.Base(filename) // never trust the uploaded path
	f, err := os.Create(filepath.Join(dir, clean))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// writeBundleTar streams the job bundle to w as a .tar.gz (agent download).
func (b *bundleStore) writeBundleTar(jobID string, w io.Writer) error {
	return tarDir(b.bundleDir(jobID), w)
}

// extractArtifactTar reads a .tar.gz of outputs posted by the agent and returns
// the number of files written.
func (b *bundleStore) extractArtifactTar(jobID string, r io.Reader) (int, error) {
	dir := b.artifactDir(jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	return untarDir(dir, r)
}

// listArtifacts returns the relative paths of a job's artifacts.
func (b *bundleStore) listArtifacts(jobID string) ([]string, error) {
	dir := b.artifactDir(jobID)
	var out []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, rel)
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}

// openArtifact opens one artifact file for download.
func (b *bundleStore) openArtifact(jobID, rel string) (*os.File, error) {
	clean := filepath.Clean("/" + rel) // defeat path traversal
	full := filepath.Join(b.artifactDir(jobID), clean)
	return os.Open(full)
}

// tarDir writes dir as a gzip-compressed tar to w. A missing dir yields an
// empty (but valid) archive.
func tarDir(dir string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

// untarDir extracts a gzip-compressed tar from r into dir, returning the count
// of regular files written. It rejects entries that escape dir.
func untarDir(dir string, r io.Reader) (int, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
		target := filepath.Join(dir, filepath.Clean("/"+hdr.Name))
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) &&
			target != filepath.Clean(dir) {
			return count, fmt.Errorf("tar entry escapes target dir: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return count, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return count, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return count, err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return count, err
			}
			f.Close()
			count++
		}
	}
	return count, nil
}
