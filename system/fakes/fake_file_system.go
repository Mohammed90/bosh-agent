package fakes

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	gouuid "github.com/nu7hatch/gouuid"

	bosherr "github.com/cloudfoundry/bosh-agent/errors"
	boshsys "github.com/cloudfoundry/bosh-agent/system"
)

type FakeFileType string

const (
	FakeFileTypeFile    FakeFileType = "file"
	FakeFileTypeSymlink FakeFileType = "symlink"
	FakeFileTypeDir     FakeFileType = "dir"
)

type FakeFileSystem struct {
	files     map[string]*FakeFileStats
	filesLock sync.Mutex

	HomeDirUsername string
	HomeDirHomePath string

	openFiles   map[string]*FakeFile
	OpenFileErr error

	ReadFileError       error
	readFileErrorByPath map[string]error

	WriteToFileError error
	SymlinkError     error

	MkdirAllError       error
	mkdirAllErrorByPath map[string]error

	ChownErr error
	ChmodErr error

	CopyFileError error

	RenameError    error
	RenameOldPaths []string
	RenameNewPaths []string

	RemoveAllError       error
	removeAllErrorByPath map[string]error

	ReadLinkError error

	TempFileError  error
	ReturnTempFile *os.File

	TempDirDir   string
	TempDirError error

	GlobErr  error
	globsMap map[string][][]string

	WalkErr error
}

type FakeFileStats struct {
	FileType FakeFileType

	FileMode os.FileMode
	Username string

	SymlinkTarget string

	Content []byte
}

func (stats FakeFileStats) StringContents() string {
	return string(stats.Content)
}

type FakeFileInfo struct {
	os.FileInfo
	file FakeFile
}

func (fi FakeFileInfo) Size() int64 {
	return int64(len(fi.file.Contents))
}

func (fi FakeFileInfo) IsDir() bool {
	return fi.file.Stats.FileType == FakeFileTypeDir
}

type FakeFile struct {
	path string
	fs   *FakeFileSystem

	Stats *FakeFileStats

	WriteErr error
	Contents []byte

	ReadErr   error
	ReadAtErr error
	readIndex int64

	CloseErr error

	StatErr error
}

func NewFakeFile(fs *FakeFileSystem) *FakeFile {
	return &FakeFile{fs: fs}
}

func (f *FakeFile) Write(contents []byte) (int, error) {
	if f.WriteErr != nil {
		return 0, f.WriteErr
	}

	f.fs.filesLock.Lock()
	defer f.fs.filesLock.Unlock()

	stats := f.fs.getOrCreateFile(f.path)
	stats.Content = contents

	f.Contents = contents
	return len(contents), nil
}

func (f *FakeFile) Read(b []byte) (int, error) {
	if f.readIndex >= int64(len(f.Contents)) {
		return 0, io.EOF
	}
	copy(b, f.Contents)
	f.readIndex = int64(len(f.Contents))
	return len(f.Contents), f.ReadErr
}

func (f *FakeFile) ReadAt(b []byte, offset int64) (int, error) {
	copy(b, f.Contents[offset:])
	return len(f.Contents[offset:]), f.ReadAtErr
}

func (f *FakeFile) Close() error {
	return f.CloseErr
}

func (f FakeFile) Stat() (os.FileInfo, error) {
	return FakeFileInfo{file: f}, f.StatErr
}

func NewFakeFileSystem() *FakeFileSystem {
	return &FakeFileSystem{
		files:                map[string]*FakeFileStats{},
		openFiles:            map[string]*FakeFile{},
		globsMap:             map[string][][]string{},
		readFileErrorByPath:  map[string]error{},
		removeAllErrorByPath: map[string]error{},
		mkdirAllErrorByPath:  map[string]error{},
	}
}

func (fs *FakeFileSystem) GetFileTestStat(path string) *FakeFileStats {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	return fs.files[path]
}

func (fs *FakeFileSystem) HomeDir(username string) (string, error) {
	fs.HomeDirUsername = username
	return fs.HomeDirHomePath, nil
}

func (fs *FakeFileSystem) RegisterMkdirAllError(path string, err error) {
	if _, ok := fs.mkdirAllErrorByPath[path]; ok {
		panic(fmt.Sprintf("MkdirAll error is already set for path: %s", path))
	}
	fs.mkdirAllErrorByPath[path] = err
}

func (fs *FakeFileSystem) MkdirAll(path string, perm os.FileMode) error {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	if fs.MkdirAllError != nil {
		return fs.MkdirAllError
	}

	if fs.mkdirAllErrorByPath[path] != nil {
		return fs.mkdirAllErrorByPath[path]
	}

	stats := fs.getOrCreateFile(path)
	stats.FileMode = perm
	stats.FileType = FakeFileTypeDir
	return nil
}

func (fs *FakeFileSystem) RegisterOpenFile(path string, file *FakeFile) {
	fs.openFiles[path] = file
}

func (fs *FakeFileSystem) OpenFile(path string, flag int, perm os.FileMode) (boshsys.ReadWriteCloseStater, error) {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	if fs.OpenFileErr != nil {
		return nil, fs.OpenFileErr
	}

	// Make sure to record a reference for FileExist, etc. to work
	stats := fs.getOrCreateFile(path)
	stats.FileMode = perm
	stats.FileType = FakeFileTypeFile

	if fs.openFiles[path] != nil {
		return fs.openFiles[path], nil
	}

	file := &FakeFile{
		path: path,
		fs:   fs,
	}

	return file, nil
}

func (fs *FakeFileSystem) Chown(path, username string) error {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	// check early to avoid requiring file presence
	if fs.ChownErr != nil {
		return fs.ChownErr
	}

	stats := fs.files[path]
	if stats == nil {
		return fmt.Errorf("Path does not exist: %s", path)
	}

	stats.Username = username
	return nil
}

func (fs *FakeFileSystem) Chmod(path string, perm os.FileMode) error {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	// check early to avoid requiring file presence
	if fs.ChmodErr != nil {
		return fs.ChmodErr
	}

	stats := fs.files[path]
	if stats == nil {
		return fmt.Errorf("Path does not exist: %s", path)
	}

	stats.FileMode = perm
	return nil
}

func (fs *FakeFileSystem) WriteFileString(path, content string) (err error) {
	return fs.WriteFile(path, []byte(content))
}

func (fs *FakeFileSystem) WriteFile(path string, content []byte) (err error) {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	if fs.WriteToFileError != nil {
		return fs.WriteToFileError
	}

	stats := fs.getOrCreateFile(path)
	stats.FileType = FakeFileTypeFile
	stats.Content = content
	return nil
}

func (fs *FakeFileSystem) ConvergeFileContents(path string, content []byte) (bool, error) {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	if fs.WriteToFileError != nil {
		return false, fs.WriteToFileError
	}

	stats := fs.getOrCreateFile(path)
	stats.FileType = FakeFileTypeFile

	if bytes.Compare(stats.Content, content) != 0 {
		stats.Content = content
		return true, nil
	}

	return false, nil
}

func (fs *FakeFileSystem) ReadFileString(path string) (string, error) {
	bytes, err := fs.ReadFile(path)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

func (fs *FakeFileSystem) RegisterReadFileError(path string, err error) {
	if _, ok := fs.readFileErrorByPath[path]; ok {
		panic(fmt.Sprintf("ReadFile error is already set for path: %s", path))
	}
	fs.readFileErrorByPath[path] = err
}

func (fs *FakeFileSystem) ReadFile(path string) ([]byte, error) {
	stats := fs.GetFileTestStat(path)
	if stats != nil {
		if fs.ReadFileError != nil {
			return nil, fs.ReadFileError
		}

		if fs.readFileErrorByPath[path] != nil {
			return nil, fs.readFileErrorByPath[path]
		}

		return stats.Content, nil
	}
	return nil, errors.New("File not found")
}

func (fs *FakeFileSystem) FileExists(path string) bool {
	return fs.GetFileTestStat(path) != nil
}

func (fs *FakeFileSystem) Rename(oldPath, newPath string) error {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	if fs.RenameError != nil {
		return fs.RenameError
	}

	if fs.files[filepath.Dir(newPath)] == nil {
		return errors.New("Parent directory does not exist")
	}

	stats := fs.files[oldPath]
	if stats == nil {
		return errors.New("Old path did not exist")
	}

	fs.RenameOldPaths = append(fs.RenameOldPaths, oldPath)
	fs.RenameNewPaths = append(fs.RenameNewPaths, newPath)

	newStats := fs.getOrCreateFile(newPath)
	newStats.Content = stats.Content
	newStats.FileMode = stats.FileMode
	newStats.FileType = stats.FileType

	// Ignore error from RemoveAll
	fs.removeAll(oldPath)

	return nil
}

func (fs *FakeFileSystem) Symlink(oldPath, newPath string) (err error) {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	if fs.SymlinkError == nil {
		stats := fs.getOrCreateFile(newPath)
		stats.FileType = FakeFileTypeSymlink
		stats.SymlinkTarget = oldPath
		return
	}

	err = fs.SymlinkError
	return
}

func (fs *FakeFileSystem) ReadLink(symlinkPath string) (string, error) {
	if fs.ReadLinkError != nil {
		return "", fs.ReadLinkError
	}

	stat := fs.GetFileTestStat(symlinkPath)
	if stat != nil {
		return stat.SymlinkTarget, nil
	}

	return "", os.ErrNotExist
}

func (fs *FakeFileSystem) CopyFile(srcPath, dstPath string) error {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	if fs.CopyFileError != nil {
		return fs.CopyFileError
	}

	fs.files[dstPath] = fs.files[srcPath]
	return nil
}

func (fs *FakeFileSystem) TempFile(prefix string) (file *os.File, err error) {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	if fs.TempFileError != nil {
		return nil, fs.TempFileError
	}

	if fs.ReturnTempFile != nil {
		file = fs.ReturnTempFile
	} else {
		file, err = os.Open("/dev/null")
		if err != nil {
			err = bosherr.WrapError(err, "Opening /dev/null")
			return
		}
	}

	// Make sure to record a reference for FileExist, etc. to work
	stats := fs.getOrCreateFile(file.Name())
	stats.FileType = FakeFileTypeFile
	return
}

func (fs *FakeFileSystem) TempDir(prefix string) (string, error) {
	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	if fs.TempDirError != nil {
		return "", fs.TempDirError
	}

	var path string
	if len(fs.TempDirDir) > 0 {
		path = fs.TempDirDir
	} else {
		uuid, err := gouuid.NewV4()
		if err != nil {
			return "", err
		}

		path = uuid.String()
	}

	// Make sure to record a reference for FileExist, etc. to work
	stats := fs.getOrCreateFile(path)
	stats.FileType = FakeFileTypeDir

	return path, nil
}

func (fs *FakeFileSystem) RegisterRemoveAllError(path string, err error) {
	if _, ok := fs.removeAllErrorByPath[path]; ok {
		panic(fmt.Sprintf("RemoveAll error is already set for path: %s", path))
	}
	fs.removeAllErrorByPath[path] = err
}

func (fs *FakeFileSystem) RemoveAll(path string) error {
	if path == "" {
		panic("RemoveAll requires path")
	}

	fs.filesLock.Lock()
	defer fs.filesLock.Unlock()

	if fs.RemoveAllError != nil {
		return fs.RemoveAllError
	}

	if fs.removeAllErrorByPath[path] != nil {
		return fs.removeAllErrorByPath[path]
	}

	return fs.removeAll(path)
}

func (fs *FakeFileSystem) removeAll(path string) error {
	filesToRemove := []string{}

	for name := range fs.files {
		if strings.HasPrefix(name, path) {
			filesToRemove = append(filesToRemove, name)
		}
	}
	for _, name := range filesToRemove {
		delete(fs.files, name)
	}
	return nil
}

func (fs *FakeFileSystem) Glob(pattern string) (matches []string, err error) {
	remainingMatches, found := fs.globsMap[pattern]
	if found {
		matches = remainingMatches[0]
		if len(remainingMatches) > 1 {
			fs.globsMap[pattern] = remainingMatches[1:]
		}
	} else {
		matches = []string{}
	}
	return matches, fs.GlobErr
}

func (fs *FakeFileSystem) Walk(root string, walkFunc filepath.WalkFunc) error {
	var paths []string
	for path := range fs.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		fileStats := fs.files[path]
		if strings.HasPrefix(path, root) {
			fakeFile := NewFakeFile(fs)
			fakeFile.Stats = fileStats
			fileInfo, _ := fakeFile.Stat()
			err := walkFunc(path, fileInfo, nil)
			if err != nil {
				return err
			}
		}
	}

	return fs.WalkErr
}

func (fs *FakeFileSystem) SetGlob(pattern string, matches ...[]string) {
	fs.globsMap[pattern] = matches
}

func (fs *FakeFileSystem) getOrCreateFile(path string) *FakeFileStats {
	stats := fs.files[path]
	if stats == nil {
		stats = new(FakeFileStats)
		fs.files[path] = stats
	}
	return stats
}
