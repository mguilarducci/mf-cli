// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	errAssetNotFound   = errors.New("no matching release asset")
	errUnverifiedAsset = errors.New("release asset cannot be verified")
)

const (
	checksumsFileName  = "checksums.txt"
	maxPluginAssetSize = 512 << 20
	maxChecksumsSize   = 1 << 20
	maxAssetRedirects  = 10
	schemeHTTPS        = "https"
	schemeHTTP         = "http"
	darwinGOOS         = "darwin"
)

// requireSecureAssetURL refuses plaintext downloads except from loopback
// (tests): the asset and its checksums share an origin, so a network attacker
// could replace both.
func requireSecureAssetURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse download URL %q: %w", redactURL(rawURL), err)
	}
	switch strings.ToLower(u.Scheme) {
	case schemeHTTPS:
		return nil
	case schemeHTTP:
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("refusing to download %s over plaintext HTTP", redactURL(rawURL))
	default:
		return fmt.Errorf("plugin downloads must use https; %q has scheme %q", redactURL(rawURL), u.Scheme)
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

//nolint:gochecknoglobals // shared client with redirect policy
var pluginHTTPClient = &http.Client{
	Timeout: 5 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxAssetRedirects {
			return fmt.Errorf("stopped after %d redirects", maxAssetRedirects)
		}
		return requireSecureAssetURL(req.URL.String())
	},
}

// releaseAssetBaseURL is where a forge serves a tag's release assets.
func releaseAssetBaseURL(repoURL, tag string) (string, error) {
	u, err := url.Parse(strings.TrimSuffix(repoURL, ".git"))
	if err != nil {
		return "", fmt.Errorf("parse repo URL: %w", err)
	}
	if u.Scheme != schemeHTTPS && u.Scheme != schemeHTTP {
		return "", fmt.Errorf("release downloads need an http(s) repo URL, got %q (declare download_url in %s)",
			redactURL(repoURL), pluginMetadataFileName)
	}
	base := strings.TrimSuffix(u.String(), "/")
	host := strings.ToLower(u.Hostname())
	if host == "gitlab.com" || strings.HasPrefix(host, "gitlab.") {
		return base + "/-/releases/" + url.PathEscape(tag) + "/downloads/", nil
	}
	return base + "/releases/download/" + url.PathEscape(tag) + "/", nil
}

func expandDownloadTemplate(tmpl, name, tag, asset string) string {
	return strings.NewReplacer(
		"{name}", name,
		"{tag}", tag,
		"{version}", strings.TrimPrefix(tag, "v"),
		"{os}", runtime.GOOS,
		"{arch}", runtime.GOARCH,
		"{asset}", asset,
	).Replace(tmpl)
}

func archAliases(goarch string) []string {
	switch goarch {
	case "amd64":
		return []string{"amd64", "x86_64"}
	case "arm64":
		return []string{"arm64", "aarch64"}
	default:
		return []string{goarch}
	}
}

func osAliases(goos string) []string {
	if goos == darwinGOOS {
		return []string{"darwin", "macos"}
	}
	return []string{goos}
}

// assetCandidates lists the archive names a release may use for this
// platform, most specific first: mf-<name>_<version>_<os>_<arch> and
// mf-<name>_<os>_<arch>, as .tar.gz, .zip or a bare binary.
func assetCandidates(name, tag string) []string {
	binName := pluginBinaryPrefix + name
	version := strings.TrimPrefix(tag, "v")
	var stems []string
	for _, osName := range osAliases(runtime.GOOS) {
		arches := archAliases(runtime.GOARCH)
		if runtime.GOOS == darwinGOOS {
			arches = append(arches, "all") // universal binaries
		}
		for _, arch := range arches {
			stems = append(stems,
				fmt.Sprintf("%s_%s_%s_%s", binName, version, osName, arch),
				fmt.Sprintf("%s_%s_%s", binName, osName, arch),
			)
		}
	}
	exts := []string{".tar.gz", ".zip", ""}
	if runtime.GOOS == windowsGOOS {
		exts = []string{".zip", ".tar.gz", ".exe"}
	}
	var out []string
	for _, ext := range exts {
		for _, stem := range stems {
			out = append(out, stem+ext)
		}
	}
	return out
}

func checksumCandidates(name, tag string) []string {
	binName := pluginBinaryPrefix + name
	return []string{
		checksumsFileName,
		fmt.Sprintf("%s_%s_checksums.txt", binName, strings.TrimPrefix(tag, "v")),
		binName + "_checksums.txt",
	}
}

// parseChecksums reads sha256sum output: "<hex>  <file>" per line.
func parseChecksums(data []byte) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		out[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return out
}

type fetchedAsset struct {
	Path     string
	Asset    string
	SHA256   string
	Verified bool
}

// downloadPluginAsset finds this platform's asset through the release's
// checksums file and downloads it, verified. Without a checksums file the
// asset is refused unless allowUnverified.
func downloadPluginAsset(ctx context.Context, meta *PluginMetadata, repoURL, name, tag, stagingDir string, allowUnverified bool) (*fetchedAsset, error) {
	var assetURL func(asset string) string
	if meta != nil && meta.DownloadURL != "" {
		assetURL = func(asset string) string { return expandDownloadTemplate(meta.DownloadURL, name, tag, asset) }
	} else {
		base, err := releaseAssetBaseURL(repoURL, tag)
		if err != nil {
			return nil, err
		}
		assetURL = func(asset string) string { return base + asset }
	}

	for _, cs := range checksumCandidates(name, tag) {
		data, err := httpGetSmall(ctx, assetURL(cs))
		if err != nil {
			continue
		}
		sums := parseChecksums(data)
		for _, asset := range assetCandidates(name, tag) {
			if digest, ok := sums[asset]; ok {
				return fetchAndVerify(ctx, assetURL(asset), asset, digest, stagingDir)
			}
		}
	}

	for _, asset := range assetCandidates(name, tag) {
		fa, err := fetchAndVerify(ctx, assetURL(asset), asset, "", stagingDir)
		switch {
		case errors.Is(err, errAssetNotFound):
			continue
		case err != nil:
			return nil, err
		case allowUnverified:
			return fa, nil
		}
		_ = os.Remove(fa.Path)
		return nil, fmt.Errorf("%w: %s %s publishes %s but no %s; ask the author to publish checksums, or install with --allow-unverified",
			errUnverifiedAsset, name, tag, asset, checksumsFileName)
	}
	return nil, fmt.Errorf("%w for %s/%s at %s %s", errAssetNotFound, runtime.GOOS, runtime.GOARCH, redactURL(repoURL), tag)
}

func httpGet(ctx context.Context, rawURL string) (*http.Response, error) {
	if err := requireSecureAssetURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", redactURL(rawURL), err)
	}
	resp, err := pluginHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", redactURL(rawURL), err)
	}
	return resp, nil
}

func httpGetSmall(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpGet(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", redactURL(rawURL), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumsSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", redactURL(rawURL), err)
	}
	if len(data) > maxChecksumsSize {
		return nil, fmt.Errorf("%s is larger than %d bytes", redactURL(rawURL), maxChecksumsSize)
	}
	return data, nil
}

// fetchAndVerify downloads rawURL into stagingDir. A non-empty wantDigest must
// match the file's sha256. A 404 is errAssetNotFound so callers can try the
// next candidate.
func fetchAndVerify(ctx context.Context, rawURL, asset, wantDigest, stagingDir string) (*fetchedAsset, error) {
	resp, err := httpGet(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", errAssetNotFound, redactURL(rawURL))
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GET %s: %s", redactURL(rawURL), resp.Status)
	}

	dst := filepath.Join(stagingDir, path.Base(asset))
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // inside our staging dir
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", dst, err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxPluginAssetSize+1))
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		return nil, fmt.Errorf("download %s: %w", asset, copyErr)
	case closeErr != nil:
		return nil, fmt.Errorf("write %s: %w", asset, closeErr)
	case n > maxPluginAssetSize:
		return nil, fmt.Errorf("%s is larger than %d bytes", asset, maxPluginAssetSize)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if wantDigest != "" && got != wantDigest {
		return nil, fmt.Errorf("checksum mismatch for %s: expected %s, got %s", asset, wantDigest, got)
	}
	return &fetchedAsset{Path: dst, Asset: asset, SHA256: got, Verified: wantDigest != ""}, nil
}

// extractPluginBinary writes the archive's mf-<name> entry to dst, executable.
// A bare (non-archive) asset is the binary itself.
func extractPluginBinary(assetPath, name, dst string) error {
	binName := pluginBinaryPrefix + name
	lower := strings.ToLower(assetPath)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractFromTarGz(assetPath, binName, dst)
	case strings.HasSuffix(lower, ".zip"):
		return extractFromZip(assetPath, binName, dst)
	default:
		src, err := os.Open(assetPath) //nolint:gosec // our own staging file
		if err != nil {
			return fmt.Errorf("open asset: %w", err)
		}
		defer func() { _ = src.Close() }()
		return writeExecutableFile(dst, src)
	}
}

func matchesPluginBinary(entryName, binName string) bool {
	base := path.Base(filepath.ToSlash(entryName))
	return base == binName || (runtime.GOOS == windowsGOOS && strings.EqualFold(base, binName+".exe"))
}

func extractFromTarGz(assetPath, binName, dst string) error {
	f, err := os.Open(assetPath) //nolint:gosec // our own staging file
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("archive has no %s", binName)
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && matchesPluginBinary(hdr.Name, binName) {
			return writeExecutableFile(dst, io.LimitReader(tr, maxPluginAssetSize))
		}
	}
}

func extractFromZip(assetPath, binName, dst string) error {
	zr, err := zip.OpenReader(assetPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer func() { _ = zr.Close() }()
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || !matchesPluginBinary(zf.Name, binName) {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("open %s in zip: %w", zf.Name, err)
		}
		defer func() { _ = rc.Close() }()
		return writeExecutableFile(dst, io.LimitReader(rc, maxPluginAssetSize))
	}
	return fmt.Errorf("archive has no %s", binName)
}

func writeExecutableFile(dst string, src io.Reader) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // plugins must be executable
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		return fmt.Errorf("write %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}
