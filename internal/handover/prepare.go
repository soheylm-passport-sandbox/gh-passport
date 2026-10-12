package handover

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ValidateProject executes nothing and refuses changed, unknown or linked files.
func ValidateProject(root string) error {
	expected := map[string][]byte{".handover-profile": []byte(Contract.ID + "\n"), ".git/HEAD": []byte(Contract.Revision + "\n"), ".git/config": []byte(Contract.GitConfig)}
	for name, value := range Contract.Files {
		expected[name] = []byte(value)
	}
	objectPaths := map[string][]byte{}
	for sha, encoded := range Contract.Objects {
		value, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return err
		}
		objectPaths[".git/objects/"+sha[:2]+"/"+sha[2:]] = value
	}
	seen := map[string]bool{}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("handover project is missing or linked")
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("handover project contains a link")
		}
		if entry.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		fileInfo, e := entry.Info()
		if e != nil || !fileInfo.Mode().IsRegular() {
			return errors.New("handover project contains a non-regular file")
		}
		if fileInfo.Size() > 1<<20 {
			return errors.New("handover project file is too large")
		}
		if rel == ".git/index" {
			return nil
		} // index is platform-specific; it is never executed by validation.
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if want, ok := objectPaths[rel]; ok {
			z, e := zlib.NewReader(bytes.NewReader(raw))
			if e != nil {
				return e
			}
			decoded, e := io.ReadAll(io.LimitReader(z, 1<<20))
			z.Close()
			if e != nil || !bytes.Equal(decoded, want) {
				return errors.New("handover project Git identity changed")
			}
			seen[rel] = true
			return nil
		}
		want, ok := expected[rel]
		if !ok || !bytes.Equal(raw, want) {
			return errors.New("handover project has changed; preserve it and ask for help")
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	for rel := range expected {
		if !seen[rel] {
			return errors.New("handover project file is missing")
		}
	}
	for rel := range objectPaths {
		if !seen[rel] {
			return errors.New("handover project Git object is missing")
		}
	}
	return nil
}

// Prepare creates a new owned project once. Errors leave an incomplete folder,
// rather than deleting/replacing work. Existing projects are inspected only.
func Prepare(ctx context.Context, root string) error {
	parent := filepath.Dir(root)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("handover parent is missing or unsafe")
	}
	if err = os.Mkdir(root, 0700); os.IsExist(err) {
		return ValidateProject(root)
	} else if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, ".handover-incomplete"), []byte(Contract.ID), 0600); err != nil {
		return err
	}
	for _, name := range []string{".git-template", ".no-hooks"} {
		if err = os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			return err
		}
	}
	for name, value := range Contract.Files {
		target := filepath.Join(root, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err = os.WriteFile(target, []byte(value), 0600); err != nil {
			return err
		}
	}
	run := func(input string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=" + filepath.Join(root, ".no-hooks"), "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		cmd.Stdin = strings.NewReader(input)
		env := os.Environ()
		for _, key := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
			kept := []string{}
			for _, value := range env {
				if !strings.HasPrefix(value, key+"=") {
					kept = append(kept, value)
				}
			}
			env = kept
		}
		cmd.Env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Synthetic Maintainer", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic Maintainer", "GIT_COMMITTER_EMAIL=synthetic@example.invalid", "GIT_AUTHOR_DATE=2000-01-01T00:00:00+00:00", "GIT_COMMITTER_DATE=2000-01-01T00:00:00+00:00")
		out, e := cmd.Output()
		if e != nil {
			return "", errors.New("fixed handover Git preparation failed; preserve the incomplete folder")
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err = run("", "init", "--quiet", "--object-format=sha1", "--template="+filepath.Join(root, ".git-template")); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, ".git/config"), []byte(Contract.GitConfig), 0600); err != nil {
		return err
	}
	paths := []string{}
	for name := range Contract.Files {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	if _, err = run("", append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}
	tree, err := run("", "write-tree")
	if err != nil || tree != Contract.Tree {
		return errors.New("fixed handover tree differs from its trusted inventory")
	}
	revision, err := run("Synthetic handover fixture v1\n", "commit-tree", tree)
	if err != nil || revision != Contract.Revision {
		return errors.New("fixed handover revision differs from its trusted inventory")
	}
	if err = os.WriteFile(filepath.Join(root, ".git/HEAD"), []byte(revision+"\n"), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, ".handover-profile"), []byte(Contract.ID+"\n"), 0600); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(root, ".handover-incomplete")); err != nil {
		return err
	} // only this marker, in the folder created above.
	return ValidateProject(root)
}
