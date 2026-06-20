package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Settings manages server configuration
type Settings struct {
	mu       sync.RWMutex
	settings map[string]string
}

func NewSettings() *Settings { return &Settings{settings: make(map[string]string)} }

func (s *Settings) Load(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()
	loaded := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			loaded[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = loaded
	s.mu.Unlock()
	return nil
}

func (s *Settings) Save(filename string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	for key, value := range s.settings {
		if _, err := fmt.Fprintf(file, "%s=%s\n", key, value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Settings) Get(key string) string { s.mu.RLock(); defer s.mu.RUnlock(); return s.settings[key] }
func (s *Settings) Set(key, value string) { s.mu.Lock(); defer s.mu.Unlock(); s.settings[key] = value }

func (s *Settings) GetInt(key string, defaultValue int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if val, ok := s.settings[key]; ok {
		var result int
		if _, err := fmt.Sscanf(val, "%d", &result); err == nil {
			return result
		}
	}
	return defaultValue
}

func (s *Settings) GetBool(key string, defaultValue bool) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if val, ok := s.settings[key]; ok {
		return strings.ToLower(val) == "true" || val == "1"
	}
	return defaultValue
}

func (s *Settings) Exists(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.settings[key]
	return ok
}
func (s *Settings) GetAll() map[string]string { s.mu.RLock(); defer s.mu.RUnlock(); return s.settings }
func (s *Settings) LoadFromString(data string) error {
	loaded := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			loaded[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = loaded
	s.mu.Unlock()
	return nil
}

// Logger handles server logging
type Logger struct {
	file      *os.File
	mu        sync.Mutex
	prefix    string
	logToFile bool
}

func NewLogger(prefix string, logToFile bool) *Logger {
	return &Logger{prefix: prefix, logToFile: logToFile}
}

func (l *Logger) Open(filename string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.logToFile {
		return nil
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	l.file = file
	return nil
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *Logger) Write(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	message = strings.TrimRight(message, "\n")
	lines := strings.Split(message, "\n")
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(fmt.Sprintf("%s %s%s\n", time.Now().Format("[03:04 PM]"), l.prefix, line))
	}
	fullMessage := builder.String()
	fmt.Print(fullMessage)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.WriteString(fullMessage)
	}
}

func (l *Logger) Error(format string, args ...interface{})   { l.Write("[ERROR] "+format, args...) }
func (l *Logger) Warning(format string, args ...interface{}) { l.Write("[WARNING] "+format, args...) }
func (l *Logger) Info(format string, args ...interface{})    { l.Write("[INFO] "+format, args...) }
func (l *Logger) Debug(format string, args ...interface{}) {
	if !DEBUG_MODE {
		return
	}
	l.Write("[DEBUG] "+format, args...)
}
func (l *Logger) PacketDebug(format string, args ...interface{}) {
	if !DEBUG_MODE || !PACKET_DEBUG_MODE {
		return
	}
	l.Write("[DEBUG] "+format, args...)
}

// FileSystem handles file operations
type FileSystem struct {
	basePath string
	mu       sync.RWMutex
	cache    map[string][]byte
}

func NewFileSystem(basePath string) *FileSystem {
	return &FileSystem{basePath: basePath, cache: make(map[string][]byte)}
}

func (fs *FileSystem) SetBasePath(path string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.basePath = path
}
func (fs *FileSystem) GetBasePath() string { fs.mu.RLock(); defer fs.mu.RUnlock(); return fs.basePath }
func (fs *FileSystem) ResolvePath(path string) string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return filepath.Join(fs.basePath, path)
}

func (fs *FileSystem) ResolveExistingPath(path string) string {
	fs.mu.RLock()
	basePath := fs.basePath
	fs.mu.RUnlock()
	if strings.Trim(filepath.ToSlash(path), "/") == "" {
		worldPath := filepath.Join(basePath, "world")
		if _, err := os.Stat(worldPath); err == nil {
			return worldPath
		}
	}
	direct := filepath.Join(basePath, path)
	if _, err := os.Stat(direct); err == nil {
		return direct
	}
	if strings.HasPrefix(filepath.ToSlash(path), "world/") {
		return direct
	}
	worldPath := filepath.Join(basePath, "world", path)
	if _, err := os.Stat(worldPath); err == nil {
		return worldPath
	}
	return direct
}

func (fs *FileSystem) FileExists(path string) bool {
	if _, err := os.Stat(fs.ResolveExistingPath(path)); err == nil {
		return true
	}
	return false
}

func (fs *FileSystem) LoadFile(path string) ([]byte, error) {
	return os.ReadFile(fs.ResolveExistingPath(path))
}

func (fs *FileSystem) SaveFile(path string, data []byte) error {
	fullPath := fs.ResolvePath(path)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, data, 0644)
}

func (fs *FileSystem) DeleteFile(path string) error { return os.Remove(fs.ResolvePath(path)) }

func (fs *FileSystem) ListFiles(path string) ([]string, error) {
	entries, err := os.ReadDir(fs.ResolveExistingPath(path))
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, entry.Name())
		}
	}
	return files, nil
}

func (fs *FileSystem) ListDirs(path string) ([]string, error) {
	entries, err := os.ReadDir(fs.ResolveExistingPath(path))
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	return dirs, nil
}

func (fs *FileSystem) FileModTime(path string) (time.Time, error) {
	info, err := os.Stat(fs.ResolveExistingPath(path))
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func (fs *FileSystem) FileInfo(path string) (os.FileInfo, error) {
	return os.Stat(fs.ResolveExistingPath(path))
}

func (fs *FileSystem) FileSize(path string) (int64, error) {
	info, err := fs.FileInfo(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (fs *FileSystem) CacheFile(path string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	data, err := fs.LoadFile(path)
	if err != nil {
		return err
	}
	fs.cache[path] = data
	return nil
}

func (fs *FileSystem) GetCached(path string) ([]byte, bool) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	data, ok := fs.cache[path]
	return data, ok
}

func (fs *FileSystem) ClearCache() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.cache = make(map[string][]byte)
}

func (fs *FileSystem) LoadFileAsLines(path string) ([]string, error) {
	data, err := fs.LoadFile(path)
	if err != nil {
		return nil, err
	}
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func (fs *FileSystem) SaveLinesAsFile(path string, lines []string) error {
	fullPath := fs.ResolvePath(path)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	file, err := os.Create(fullPath)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, line := range lines {
		if _, err := writer.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func (fs *FileSystem) CopyFile(src, dst string) error {
	srcPath, dstPath := fs.ResolvePath(src), fs.ResolvePath(dst)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()
	dstFile, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dstFile.Close()
	_, err = io.Copy(dstFile, srcFile)
	return err
}
