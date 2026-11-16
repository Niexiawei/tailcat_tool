package appmeta

import (
	"context"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/avast/apkparser"
)

const (
	// maxAPKSize is the largest APK the store will pull off a device.
	maxAPKSize = 400 << 20
	// maxIconBytes is the largest icon file it will keep.
	maxIconBytes = 1 << 20

	adbMetaTimeout = 10 * time.Second
	apkPullTimeout = 60 * time.Second

	// failedRetryAfter keeps a package with no readable APK (a system process,
	// a package the shell cannot see) from being asked about every two seconds.
	failedRetryAfter = 10 * time.Minute
)

// Store is the application metadata cache: it pulls the APK of each running
// application off the device, reads its name and icon, and keeps them for a
// week. It implements both core.AppMetaSource and core.AppMetaCache.
type Store struct {
	runner  core.FileRunner
	cache   *cache
	now     func() time.Time
	queue   chan job
	wg      sync.WaitGroup
	mu      sync.Mutex
	pending map[string]bool
	forced  map[string]bool
	// failed remembers when a package last failed, keyed like pending.
	failed map[string]time.Time
}

type job struct {
	device, adb, serial, pkg string
	force                    bool
}

// New opens the store over dir, which it creates when missing. workers bounds
// how many APKs are read at once; one is enough and leaves the channel alone.
func New(dir string, runner core.FileRunner, workers int) (*Store, error) {
	if runner == nil {
		return nil, core.Error("未提供命令执行器，无法读取应用信息")
	}
	if workers <= 0 {
		workers = 1
	}
	c, err := newCache(dir, time.Now)
	if err != nil {
		return nil, err
	}
	s := &Store{
		runner:  runner,
		cache:   c,
		now:     time.Now,
		queue:   make(chan job, 512),
		pending: map[string]bool{},
		forced:  map[string]bool{},
		failed:  map[string]time.Time{},
	}
	for i := 0; i < workers; i++ {
		go s.work()
	}
	return s, nil
}

// Lookup returns what the cache knows, marked stale when past its lifetime.
func (s *Store) Lookup(device, pkg string) (core.AppMeta, bool) {
	return s.cache.lookupAt(device, pkg, s.now())
}

// Request queues the packages without a fresh entry.
func (s *Store) Request(device, adbPath, serial string, pkgs []string) {
	s.enqueue(device, adbPath, serial, pkgs, false)
}

// Refresh queues the packages whatever the cache says about them.
func (s *Store) Refresh(device, adbPath, serial string, pkgs []string) {
	s.enqueue(device, adbPath, serial, pkgs, true)
}

// Entries lists one device's cached applications for the cache page.
func (s *Store) Entries(device string) []core.AppMetaEntry {
	return s.cache.entriesAt(device, s.now())
}

// Invalidate drops one application from the cache.
func (s *Store) Invalidate(device, pkg string) error { return s.cache.invalidate(device, pkg) }

// Purge drops everything cached for one device.
func (s *Store) Purge(device string) error { return s.cache.purge(device) }

// waitIdle blocks until the queue is empty; it is how the tests observe a
// fetch that would otherwise finish whenever the scheduler felt like it.
func (s *Store) waitIdle() { s.wg.Wait() }

func (s *Store) enqueue(device, adbPath, serial string, pkgs []string, force bool) {
	now := s.now()
	var jobs []job
	s.mu.Lock()
	for _, pkg := range pkgs {
		if pkg == "" {
			continue
		}
		key := entryKey(device, pkg)
		if force {
			// Whatever is queued may have skipped this package as fresh, so
			// let the worker know it must read the APK this time.
			s.forced[key] = true
			delete(s.failed, key)
		} else {
			if meta, ok := s.cache.lookupAt(device, pkg, now); ok && !meta.Stale {
				continue
			}
			if last, failed := s.failed[key]; failed && now.Sub(last) < failedRetryAfter {
				continue
			}
		}
		if s.pending[key] {
			continue
		}
		s.pending[key] = true
		jobs = append(jobs, job{device: device, adb: adbPath, serial: serial, pkg: pkg, force: force})
	}
	s.wg.Add(len(jobs))
	s.mu.Unlock()

	for _, queued := range jobs {
		s.queue <- queued
	}
}

func (s *Store) work() {
	for queued := range s.queue {
		key := entryKey(queued.device, queued.pkg)
		s.mu.Lock()
		force := queued.force || s.forced[key]
		delete(s.forced, key)
		s.mu.Unlock()

		stored := s.fetch(queued, force)

		s.mu.Lock()
		delete(s.pending, key)
		if stored {
			delete(s.failed, key)
		} else {
			s.failed[key] = s.now()
		}
		s.mu.Unlock()
		s.wg.Done()
	}
}

// fetch reads one application's APK and stores its name and icon. It reports
// whether the cache now holds an entry, so a package with nothing to read backs
// off instead of being asked about on every round.
func (s *Store) fetch(queued job, force bool) bool {
	apk, err := s.apkPath(queued)
	if err != nil || apk == "" {
		return false
	}
	size, stamp := s.apkIdentity(queued, apk)
	if size > maxAPKSize {
		return false
	}
	key := queued.pkg + "|" + apk + "|" + strconv.FormatInt(size, 10) + "|" + stamp
	if !force && s.cache.freshAt(queued.device, queued.pkg, key, s.now()) {
		return true
	}
	label, icon := s.readApk(queued, apk)
	if label == "" {
		return false
	}
	if err := s.cache.putAt(queued.device, queued.pkg, key, label, icon, s.now()); err != nil {
		return false
	}
	return true
}

// apkPath asks the device where a package's base APK lives.
func (s *Store) apkPath(queued job) (string, error) {
	res, err := s.runner.Run(context.Background(), core.Command{
		Path:    queued.adb,
		Args:    []string{"-s", queued.serial, "shell", "pm path " + queued.pkg},
		Timeout: adbMetaTimeout,
	}, nil)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.ReplaceAll(res.Output, "\r", ""), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "package:"); ok {
			return path, nil
		}
	}
	return "", core.Error("设备上没有这个应用")
}

// apkIdentity is the size and modification time that identify one APK build.
// A failed stat just means the entry is keyed by its path alone.
func (s *Store) apkIdentity(queued job, apk string) (int64, string) {
	res, err := s.runner.Run(context.Background(), core.Command{
		Path:    queued.adb,
		Args:    []string{"-s", queued.serial, "shell", "stat -c '%s %Y' " + apk},
		Timeout: adbMetaTimeout,
	}, nil)
	if err != nil {
		return 0, "unknown"
	}
	fields := strings.Fields(res.Output)
	if len(fields) == 0 {
		return 0, "unknown"
	}
	size, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, "unknown"
	}
	stamp := "unknown"
	if len(fields) > 1 {
		stamp = fields[1]
	}
	return size, stamp
}

// readApk pulls the APK into a temporary file and reads its manifest.
func (s *Store) readApk(queued job, apk string) (string, []byte) {
	dir, err := os.MkdirTemp("", "tailcat-apk-")
	if err != nil {
		return "", nil
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "base.apk")
	res, err := s.runner.RunToFile(context.Background(), core.Command{
		Path:    queued.adb,
		Args:    []string{"-s", queued.serial, "exec-out", "cat", apk},
		Timeout: apkPullTimeout,
	}, path)
	if err != nil || res.ExitCode != 0 {
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 || info.Size() > maxAPKSize {
		return "", nil
	}
	return readManifest(path)
}

// readManifest reads the label and icon out of one APK. The APK is untrusted
// input from another machine, so a panic in the parser costs the metadata, not
// the process.
func readManifest(path string) (label string, icon []byte) {
	defer func() {
		if recover() != nil {
			label, icon = "", nil
		}
	}()

	file, err := os.Open(path)
	if err != nil {
		return "", nil
	}
	defer file.Close()
	zip, err := apkparser.OpenZipReader(file)
	if err != nil {
		return "", nil
	}
	defer zip.Close()

	scanner := &manifestScanner{}
	if _, manifestErr := apkparser.ParseApkWithZip(zip, scanner); manifestErr != nil {
		return "", nil
	}
	label = cleanLabel(scanner.label)
	if label == "" {
		return "", nil
	}
	icon = readIcon(zip, scanner.icon)
	return label, icon
}

// readIcon reads the icon file the manifest points at. An adaptive icon is an
// XML drawable, which needs a renderer we do not have, so it is left out: the
// page then shows the application's initials instead.
func readIcon(zip *apkparser.ZipReader, name string) []byte {
	if !isImageFile(name) {
		return nil
	}
	file := zip.File[name]
	if file == nil {
		return nil
	}
	if err := file.Open(); err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxIconBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxIconBytes {
		return nil
	}
	return data
}

// manifestScanner picks the label and icon off the <application> element. The
// parser resolves resource references before they reach it, so both arrive as
// plain values.
type manifestScanner struct {
	label string
	icon  string
}

func (m *manifestScanner) EncodeToken(token xml.Token) error {
	start, ok := token.(xml.StartElement)
	if !ok || start.Name.Local != "application" {
		return nil
	}
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "label":
			if m.label == "" {
				m.label = attr.Value
			}
		case "icon":
			m.icon = attr.Value
		}
	}
	return nil
}

func (m *manifestScanner) Flush() error { return nil }

// cleanLabel rejects what the parser hands back when it could not resolve a
// reference: a resource id is not a name the user should read.
func cleanLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" || strings.HasPrefix(label, "@") || strings.HasPrefix(label, "0x") {
		return ""
	}
	return label
}

func isImageFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".webp", ".jpg", ".jpeg":
		return true
	}
	return false
}
