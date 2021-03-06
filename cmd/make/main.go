// This is the build script for cistern. It replaces an older
// Makefile that was harder to keep compatible with all
// target platforms without relying on specific building tools
// (e.g. GNU make).
//
// This script depends on the following external programs:
//  - go to build executables
//  - git for constructing the version number based
//  on the last tag and the current state of the repository
//  - pandoc for building the manual page in various
//  formats. See https://pandoc.org/installing.html for installation
//  instructions.
//  - tar for building gzipped binary archives
//
//
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
)

const usage = `Usage:

    Test:
        make test        # Run unit tests

    Compile and release:
        make cistern     # Build executable and manual pages for host machine
        make releases    # Build all release archives and release notes
        make clean       # Remove build directory

    Help:
        make usage       # Print this help message

Note: This script relies on the following local executables: go, git, tar and pandoc.
`

// Location of the directory where build artifacts are stored
const buildDirectory = "build"

// Name or path for the go executable
var goBin = "go"

// List of OS + architecture targets for releases
var OSesByArch = map[string][]string{
	"amd64": {"linux", "freebsd", "openbsd", "netbsd", "darwin"},
}

func version(env []string) (string, error) {
	gitVersion, err := gitDescribe()
	if err != nil {
		return "", err
	}

	goOs, goArch, err := GoOsAndArch(env)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s-%s-%s", gitVersion, goOs, goArch), nil
}

// Build version number from last git tag
func gitDescribe() (string, error) {
	cmd := exec.Command("git", "describe", "--tags", "--dirty")
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	bs, err := cmd.Output()
	if err != nil {
		return "", err
	}

	version := strings.Split(string(bs), "\n")[0]
	version = strings.Trim(version, "\r")
	return version, nil
}

func goBuild(version string, dir string, env []string) error {
	version = fmt.Sprintf("-X main.Version=%s", version)
	executable := path.Join(dir, "cistern")
	fmt.Fprint(os.Stderr, fmt.Sprintf("Building %s...\n", executable))
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	cmd := exec.Command(goBin, buildDirectory, "-ldflags", version, "-o", executable, path.Join(wd, "cmd", "cistern"))
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyFile(src, dst string) error {
	inFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer inFile.Close()

	outFile, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(outFile, inFile); err != nil {
		outFile.Close()
		return err
	}

	return outFile.Close()
}

func man(dir string, version string) error {
	bs, err := ioutil.ReadFile("man.md")
	if err != nil {
		return err
	}

	markdown := strings.Replace(string(bs), "\\<version\\>", version, 1)

	output := path.Join(dir, "cistern.man.html")
	fmt.Fprintf(os.Stderr, "Building %s...\n", output)
	mdToHTML := exec.Command("pandoc", "-s", "-t", "html5", "--template", "pandoc_template.html", "-o", output)
	mdToHTML.Stdin = bytes.NewBufferString(markdown)
	mdToHTML.Stderr = os.Stderr
	if err := mdToHTML.Run(); err != nil {
		return err
	}

	output = path.Join(dir, "cistern.man.1")
	fmt.Fprintf(os.Stderr, "Building %s...\n", output)
	mdToRoff := exec.Command("pandoc", "-s", "-t", "man", "-o", output)
	mdToRoff.Stdin = bytes.NewBufferString(markdown)
	mdToRoff.Stderr = os.Stderr
	return mdToRoff.Run()
}

const licenseHeader = `Below is the license of cistern and of every package it uses


===== github.com/nbedos/cistern =====
`

func license(dir string) error {
	output := path.Join(dir, "LICENSE")
	fmt.Fprintf(os.Stderr, "Building %s...\n", output)
	b := strings.Builder{}
	b.WriteString(licenseHeader)
	bs, err := ioutil.ReadFile("LICENSE")
	if err != nil {
		return err
	}
	if _, err := b.Write(bs); err != nil {
		return err
	}

	goList := exec.Command(goBin, "list", "-f", "{{.Dir}}", "-m", "all")
	goList.Stderr = os.Stderr
	if bs, err = goList.Output(); err != nil {
		return err
	}

	for _, pkgPath := range strings.Split(string(bs), "\n") {
		if !strings.Contains(pkgPath, "cistern") {
			licensePath := path.Join(pkgPath, "LICENSE")
			if bs, err = ioutil.ReadFile(licensePath); err == nil {
				b.WriteString("\n\n")
				s := path.Join(goBin, "pkg", "mod") + string([]rune{os.PathSeparator})
				if n := strings.Index(pkgPath, s); n >= 0 {
					pkgPath = pkgPath[n+len(s):]
				}
				b.WriteString(fmt.Sprintf("===== %s =====\n", pkgPath))
				b.Write(bs)
			}
		}
	}

	return ioutil.WriteFile(output, []byte(b.String()), os.ModePerm)
}

func GoOsAndArch(env []string) (string, string, error) {
	env = append(os.Environ(), env...)
	goEnvGOOS := exec.Command(goBin, "env", "GOOS")
	goEnvGOOS.Stderr = os.Stderr
	goEnvGOOS.Env = env
	bs, err := goEnvGOOS.Output()
	if err != nil {
		return "", "", err
	}
	GoOS := strings.Trim(string(bs), "\r\n")

	goEnvGOARCH := exec.Command(goBin, "env", "GOARCH")
	goEnvGOARCH.Stderr = os.Stderr
	goEnvGOARCH.Env = env
	bs, err = goEnvGOARCH.Output()
	if err != nil {
		return "", "", err
	}
	GoArch := strings.Trim(string(bs), "\r\n")

	return GoOS, GoArch, nil
}

func build(workdir string, env []string, versionnedDir bool) (string, error) {
	version, err := version(env)
	if err != nil {
		return "", err
	}

	dir := workdir
	if versionnedDir {
		dir = path.Join(workdir, fmt.Sprintf("cistern-%s", version))
	}
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return "", err
	}
	if err := man(dir, version); err != nil {
		return "", err
	}
	if err := goBuild(version, dir, env); err != nil {
		return "", err
	}
	if err := license(dir); err != nil {
		return "", err
	}

	src := path.Join("cmd", "cistern", "cistern.toml")
	dst := path.Join(dir, "cistern.toml")
	fmt.Fprintf(os.Stderr, "Building %s...\n", dst)
	if err := copyFile(src, dst); err != nil {
		return "", err
	}

	return dir, nil
}

func compress(workdir string, dir string) (string, error) {
	archivePath := dir + ".tar.gz"
	fmt.Fprintf(os.Stderr, "Building %s...\n", path.Join(workdir, archivePath))
	absoluteArchivePath := path.Join(workdir, archivePath)
	cmd := exec.Command("tar", "-C", workdir, "-czf", absoluteArchivePath, dir)
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return absoluteArchivePath, cmd.Run()
}

func hash(filepath string) (string, error) {
	bs, err := ioutil.ReadFile(filepath)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", sha256.Sum256(bs)), nil
}

// Build binary release archives for all targets
func releases(dir string, env []string, OSesByArch map[string][]string) error {
	archives := make([]string, 0)
	for arch, OSes := range OSesByArch {
		for _, OS := range OSes {
			targetEnv := append(env, fmt.Sprintf("GOOS=%s", OS), fmt.Sprintf("GOARCH=%s", arch))
			archiveDir, err := build(dir, targetEnv, true)
			if err != nil {
				return err
			}

			archivePath, err := compress(dir, path.Base(archiveDir))
			if err != nil {
				return err
			}
			archives = append(archives, archivePath)
		}
	}

	// Match sort order of files on the GitHub release page
	sort.Strings(archives)
	if err := releaseNotes(dir, archives); err != nil {
		return err
	}

	// Build manual page, sample configuration file
	if _, err := build(dir, os.Environ(), false); err != nil {
		return err
	}

	return nil
}

func releaseNotes(dir string, archives []string) error {
	notes := path.Join(dir, "notes.md")
	fmt.Fprintf(os.Stderr, "Building %s...\n", notes)

	changelog, err := ioutil.ReadFile("CHANGELOG.md")
	if err != nil {
		return err
	}
	content := strings.Builder{}
	// Extract content between first and second occurrence of "\n## "
	parts := strings.SplitN(string(changelog), "\n## ", 3)
	if len(parts) >= 2 {
		sectionWithHeader := parts[1]
		lines := strings.SplitN(sectionWithHeader, "\n", 2)
		changes := lines[len(lines)-1]
		content.WriteString("# Changes\n")
		content.WriteString(changes + "\n")
	}

	content.WriteString("# Checksums")
	content.WriteString("\n\nfile | sha256sum\n---|---\n")
	for _, archive := range archives {
		h, err := hash(archive)
		if err != nil {
			return err
		}
		content.WriteString(fmt.Sprintf("%s | %s\n", path.Base(archive), h))
	}

	return ioutil.WriteFile(notes, []byte(content.String()), os.ModePerm)
}

func main() {
	env := []string{"GO111MODULE=on", "CGO_ENABLED=0"}

	if len(os.Args) != 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "usage":
		fmt.Fprint(os.Stderr, usage)
	case "cistern":
		_, err = build(buildDirectory, env, false)
	case "release", "releases":
		err = releases(buildDirectory, env, OSesByArch)
	case "clean":
		err = os.RemoveAll(buildDirectory)
	case "test", "tests":
		cmd := exec.Command(goBin, "test", "-v", "./...")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), env...)
		err = cmd.Run()
	default:
		fmt.Fprintf(os.Stderr, "unknow command: %q\n", os.Args[1])
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprint(os.Stderr, err.Error()+"\n")
		os.Exit(1)
	}
}
