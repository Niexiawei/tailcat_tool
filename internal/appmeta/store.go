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

	// maxWholeAPKPull bounds the fallback that downloads a whole package: past
	// this size the download costs more than the name is worth, and saying so
	// beats waiting for a 60-second timeout.
	maxWholeAPKPull = 64 << 20

	adbMetaTimeout = 10 * time.Second
	apkPullTimeout = 60 * time.Second

	// failedRetryAfter keeps a package whose read failed for a transient reason
	// (a dropped channel, an unreadable package) from being asked about on every
	// sampling round. A package the device has no APK for is Final and never
	// retried by itself.
	failedRetryAfter = 10 * time.Minute
)

// Store is the application metadata cache: it pulls the APK of each running
// application off the device, reads its name and icon, and keeps them for a
// week. It implements both core.AppMetaSource and core.AppMetaCache.
type Store struct {
	runner core.FileRunner
	cache  *cache
	now    func() time.Time
	// queue carries batches; urgent carries the single package the user just
	// asked about, so a click never waits behind a full refresh.
	queue  chan job
	urgent chan job
	wg     sync.WaitGroup
	mu     sync.Mutex
	// status is the answer to "what is happening to this package": queued,
	// named, or failed with the reason. It replaces the view's blind timer.
	status map[string]core.AppMetaStatus
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
		runner: runner,
		cache:  c,
		now:    time.Now,
		queue:  make(chan job, 512),
		urgent: make(chan job, 64),
		status: map[string]core.AppMetaStatus{},
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

// Status reports what the store knows about one package right now: a queued or
// running read, a cached name, or a failure with its reason. A cached name from
// a previous run still answers the question, so a restarted app does not offer
// to read what it already has.
func (s *Store) Status(device, pkg string) (core.AppMetaStatus, bool) {
	key := entryKey(device, pkg)
	s.mu.Lock()
	status, known := s.status[key]
	s.mu.Unlock()
	if known {
		return status, true
	}
	if meta, found := s.cache.lookupAt(device, pkg, s.now()); found {
		return core.AppMetaStatus{State: core.AppMetaReady, At: meta.FetchedAt}, true
	}
	return core.AppMetaStatus{}, false
}

// setStatus publishes one package's state; the view redraws when sampling next
// reports, so nothing else has to be signalled here.
func (s *Store) setStatus(key string, status core.AppMetaStatus) {
	s.mu.Lock()
	s.status[key] = status
	s.mu.Unlock()
}

// Forget drops the statuses this device accumulated, keeping the cached names:
// after a reconnect a process that had no APK may be a real application.
func (s *Store) Forget(device string) {
	prefix := device + "|"
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.status {
		if strings.HasPrefix(key, prefix) {
			delete(s.status, key)
		}
	}
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
	// One package is the row the user is looking at; a long list is background
	// work that must not hold that row up.
	interactive := len(pkgs) == 1
	var jobs []job
	s.mu.Lock()
	for _, pkg := range pkgs {
		if pkg == "" {
			continue
		}
		key := entryKey(device, pkg)
		current := s.status[key]
		if !force {
			switch current.State {
			case core.AppMetaPending:
				continue
			case core.AppMetaReady:
				if meta, ok := s.cache.lookupAt(device, pkg, now); ok && !meta.Stale {
					continue
				}
			case core.AppMetaFailed:
				// A settled "no APK" answer only changes if the package does, so
				// only an explicit retry asks again.
				if current.Final || now.Sub(current.At) < failedRetryAfter {
					continue
				}
			}
		}
		s.status[key] = core.AppMetaStatus{State: core.AppMetaPending, At: now}
		jobs = append(jobs, job{device: device, adb: adbPath, serial: serial, pkg: pkg, force: force})
	}
	s.wg.Add(len(jobs))
	s.mu.Unlock()

	for _, queued := range jobs {
		if interactive {
			s.urgent <- queued
			continue
		}
		s.queue <- queued
	}
}

func (s *Store) work() {
	for {
		var queued job
		// A row request that arrived while this one was being taken still goes
		// first; the inner select keeps an idle worker from missing it.
		select {
		case queued = <-s.urgent:
		default:
			select {
			case queued = <-s.urgent:
			case queued = <-s.queue:
			}
		}
		s.handle(queued)
	}
}

// handle runs one job and publishes how it ended. Every path writes a terminal
// status: a row must never be left spinning because a read went missing.
func (s *Store) handle(queued job) {
	key := entryKey(queued.device, queued.pkg)
	stored, failure := s.fetchSafely(queued)
	status := core.AppMetaStatus{State: core.AppMetaReady, At: s.now()}
	if !stored {
		status = core.AppMetaStatus{State: core.AppMetaFailed, Reason: failure.reason, Final: failure.final, At: s.now()}
	}
	s.setStatus(key, status)
	s.wg.Done()
}

// fetchFailure says why a read produced nothing, in the user's words, and
// whether trying again could ever help.
type fetchFailure struct {
	reason string
	final  bool
}

// fetchSafely turns a panic in the untrusted-APK path into a stated failure.
func (s *Store) fetchSafely(queued job) (stored bool, failure fetchFailure) {
	defer func() {
		if recovered := recover(); recovered != nil {
			stored, failure = false, fetchFailure{reason: core.ReasonAPKUnreadable}
		}
	}()
	return s.fetch(queued)
}

// fetch reads one application's APK and stores its name and icon.
func (s *Store) fetch(queued job) (bool, fetchFailure) {
	apk, failure := s.apkPath(queued)
	if failure.reason != "" {
		return false, failure
	}
	size, stamp := s.apkIdentity(queued, apk)
	if size > maxAPKSize {
		return false, fetchFailure{reason: core.ReasonAPKTooLarge}
	}
	key := queued.pkg + "|" + apk + "|" + strconv.FormatInt(size, 10) + "|" + stamp
	if !queued.force && s.cache.freshAt(queued.device, queued.pkg, key, s.now()) {
		return true, fetchFailure{}
	}
	label, icon, failure := s.readApk(queued, apk, size)
	if failure.reason != "" {
		return false, failure
	}
	if err := s.cache.putAt(queued.device, queued.pkg, key, label, icon, s.now()); err != nil {
		return false, fetchFailure{reason: core.ReasonAPKUnreadable}
	}
	return true, fetchFailure{}
}

// apkPath asks the device where a package's base APK lives. An answer with no
// `package:` line is final: this process is not an installed application, and
// only installing one or rebuilding the channel can change that.
func (s *Store) apkPath(queued job) (string, fetchFailure) {
	res, err := s.runner.Run(context.Background(), core.Command{
		Path:    queued.adb,
		Args:    []string{"-s", queued.serial, "shell", "pm path " + queued.pkg},
		Timeout: adbMetaTimeout,
	}, nil)
	if err != nil {
		return "", fetchFailure{reason: core.ReasonAPKUnreadable}
	}
	if res.TimedOut {
		return "", fetchFailure{reason: core.ReasonAPKTimeout}
	}
	for _, line := range strings.Split(strings.ReplaceAll(res.Output, "\r", ""), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "package:"); ok && path != "" {
			return path, fetchFailure{}
		}
	}
	return "", fetchFailure{reason: core.ReasonNoAppPackage, final: true}
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

// readApk reads one application's name and icon: the three entries first, the
// whole APK only as a fallback, and only when that download is affordable.
func (s *Store) readApk(queued job, apk string, size int64) (string, []byte, fetchFailure) {
	if label, icon, ok := s.readApkEntries(queued, apk); ok {
		return label, icon, fetchFailure{}
	}
	if size > maxWholeAPKPull {
		return "", nil, fetchFailure{reason: core.ReasonAPKTooLarge}
	}
	label, icon := s.readWholeApk(queued, apk)
	if label == "" {
		return "", nil, fetchFailure{reason: core.ReasonAPKUnreadable}
	}
	return label, icon, fetchFailure{}
}

// readApkEntries pulls just the three entries reading a name needs: the
// manifest, the resource table that resolves it, and the icon. Pulling a whole
// 100 MB APK for that is what made resolution take minutes over a tailcat
// channel.
func (s *Store) readApkEntries(queued job, apk string) (label string, icon []byte, ok bool) {
	defer func() {
		if recover() != nil {
			label, icon, ok = "", nil, false
		}
	}()

	dir, err := os.MkdirTemp("", "tailcat-entries-")
	if err != nil {
		return "", nil, false
	}
	defer os.RemoveAll(dir)

	manifestPath := filepath.Join(dir, "AndroidManifest.xml")
	if !s.extractEntry(queued, apk, "AndroidManifest.xml", manifestPath) {
		return "", nil, false
	}

	// resources.arsc is optional: without it the manifest's literal values
	// still resolve, only references do not.
	var resources *apkparser.ResourceTable
	arscPath := filepath.Join(dir, "resources.arsc")
	if s.extractEntry(queued, apk, "resources.arsc", arscPath) {
		if file, err := os.Open(arscPath); err == nil {
			resources, _ = apkparser.ParseResourceTable(file)
			file.Close()
		}
	}

	scanner := &manifestScanner{}
	file, err := os.Open(manifestPath)
	if err != nil {
		return "", nil, false
	}
	parseErr := apkparser.ParseXml(file, scanner, resources)
	file.Close()
	if parseErr != nil {
		return "", nil, false
	}
	label = cleanLabel(scanner.label)
	if label == "" {
		return "", nil, false
	}
	if isImageFile(scanner.icon) {
		iconPath := filepath.Join(dir, "icon")
		if s.extractEntry(queued, apk, scanner.icon, iconPath) {
			icon = readFileLimited(iconPath, maxIconBytes)
		}
	}
	return label, icon, true
}

// extractEntry writes one zip entry of the APK to path. It must go through
// `exec-out`, never `shell`: adb shell gives the remote command a PTY, and the
// tty rewrites every LF into CRLF, so a 724620-byte manifest arrives as 739233
// bytes and every binary entry is corrupt. `apkparser` then fails for every
// application, the fast path silently dies, and each read falls back to
// downloading the whole APK. A device without `unzip`, or an entry the shell
// cannot read, reports false and the caller falls back to the whole APK.
func (s *Store) extractEntry(queued job, apk, entry, path string) bool {
	res, err := s.runner.RunToFile(context.Background(), core.Command{
		Path:    queued.adb,
		Args:    []string{"-s", queued.serial, "exec-out", "unzip", "-p", apk, entry},
		Timeout: adbMetaTimeout,
	}, path)
	if err != nil || res.ExitCode != 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

func readFileLimited(path string, limit int64) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return nil
	}
	return data
}

// readWholeApk falls back to pulling the APK into a temporary file.
func (s *Store) readWholeApk(queued job, apk string) (string, []byte) {
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
