package files

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode"
)

// ErrUploadRejected wraps every upload-validation failure (extension outside
// the allowlist, magic-byte mismatch). HTTP handlers map it to 422 with the
// wrapped detail; programmatic callers errors.Is against it.
var ErrUploadRejected = errors.New("files: upload rejected")

// DefaultAllowedExtensions is the out-of-the-box upload ALLOWLIST (OWASP: an
// allowlist, never a denylist — an extension not listed here is rejected, so
// .php/.exe/.sh are unrepresentable rather than enumerated). Operators extend
// or replace it via APPITOOLS_FILES_ALLOWED_EXT; the single value "*" disables
// the check. A file with NO extension is always accepted: it cannot be
// double-click-executed, is stored under a hash key, and is served with
// attachment + nosniff.
var DefaultAllowedExtensions = []string{
	// images
	"jpg", "jpeg", "png", "gif", "webp", "svg", "avif", "ico", "bmp", "tiff", "heic",
	// documents
	"pdf", "txt", "md", "csv", "json", "xml", "yaml", "yml",
	"xlsx", "xls", "docx", "doc", "pptx", "ppt", "odt", "ods", "odp", "rtf",
	// archives
	"zip", "gz", "tgz", "tar", "7z", "rar",
	// audio / video
	"mp3", "wav", "ogg", "m4a", "flac", "mp4", "webm", "mov", "avi", "mkv",
	// fonts + generic binary
	"woff", "woff2", "ttf", "otf", "bin", "dat", "parquet",
}

// magicByExt pins extensions of well-known binary formats to the content type
// http.DetectContentType must sniff from their first bytes. If the sniff is
// CONCLUSIVE (not application/octet-stream) and disagrees, the upload is
// rejected — a PHP script renamed to photo.jpg sniffs text/plain and dies
// here. Office/zip formats share the zip signature; svg has NO sniff signature
// (DetectContentType deliberately never returns image/svg+xml) so it is absent.
var magicByExt = map[string][]string{
	"png":  {"image/png"},
	"jpg":  {"image/jpeg"},
	"jpeg": {"image/jpeg"},
	"gif":  {"image/gif"},
	"webp": {"image/webp"},
	"pdf":  {"application/pdf"},
	"zip":  {"application/zip"},
	"xlsx": {"application/zip"},
	"docx": {"application/zip"},
	"pptx": {"application/zip"},
	"gz":   {"application/x-gzip"},
	"tgz":  {"application/x-gzip"},
	"mp4":  {"video/mp4"},
	"webm": {"video/webm"},
	"avi":  {"video/avi"},
	"wav":  {"audio/wave"},
}

// sniffableFamilies are declared content-type families the sniffer can vouch
// for: when a client DECLARES one of these but the conclusive sniff lands in a
// different family, the declaration is a lie and the upload is rejected (the
// client Content-Type is never trusted — OWASP). image/svg+xml is exempt
// (sniffs as text/xml — no signature).
var sniffableFamilies = []string{"image/", "video/", "audio/", "application/pdf", "application/zip"}

// UploadPolicy is the compiled upload-validation config: built once at boot,
// applied to every Put (all ingestion paths, not just HTTP).
type UploadPolicy struct {
	allowAll bool
	exts     map[string]struct{}
}

// NewUploadPolicy compiles an extension allowlist (entries with or without the
// leading dot, case-insensitive). nil/empty → DefaultAllowedExtensions; a
// single "*" allows every extension (magic-byte and declared-type checks still
// apply).
func NewUploadPolicy(exts []string) UploadPolicy {
	if len(exts) == 0 {
		exts = DefaultAllowedExtensions
	}
	p := UploadPolicy{exts: make(map[string]struct{}, len(exts))}
	for _, e := range exts {
		e = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(e, ".")))
		if e == "*" {
			p.allowAll = true
			continue
		}
		if e != "" {
			p.exts[e] = struct{}{}
		}
	}
	return p
}

// Validate applies the OWASP upload checks to one upload and returns the
// content type to STORE (the authoritative one — never blindly the client's):
//
//  1. extension allowlist (on the sanitized name),
//  2. magic bytes vs extension (http.DetectContentType over the first 512
//     bytes; conclusive disagreement rejects),
//  3. magic bytes vs DECLARED type (a declared image/video/audio/pdf/zip whose
//     content conclusively sniffs outside that family rejects).
//
// The stored type is the declared one when it survives the checks (it is often
// more specific than the sniff — e.g. the openxml type over application/zip),
// else the sniffed type.
func (p UploadPolicy) Validate(name, declaredCT string, head []byte) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if ext != "" && !p.allowAll {
		if _, ok := p.exts[ext]; !ok {
			return "", fmt.Errorf("%w: extension %q is not allowed", ErrUploadRejected, "."+ext)
		}
	}

	sniffed := baseCT(http.DetectContentType(head))
	conclusive := sniffed != "application/octet-stream"
	declared := baseCT(declaredCT)

	if want, ok := magicByExt[ext]; ok && conclusive {
		match := false
		for _, w := range want {
			if sniffed == w {
				match = true
				break
			}
		}
		if !match {
			return "", fmt.Errorf("%w: content does not match extension %q (detected %s)",
				ErrUploadRejected, "."+ext, sniffed)
		}
	}

	if declared != "" && declared != "image/svg+xml" && conclusive {
		for _, fam := range sniffableFamilies {
			if strings.HasPrefix(declared, fam) && !strings.HasPrefix(sniffed, fam) {
				return "", fmt.Errorf("%w: declared type %q but content detected as %s",
					ErrUploadRejected, declared, sniffed)
			}
		}
	}

	if declared != "" {
		return declared, nil
	}
	return sniffed, nil
}

// baseCT strips media-type parameters ("text/plain; charset=utf-8" →
// "text/plain") and normalizes case.
func baseCT(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// SanitizeName reduces a client filename to safe METADATA at rest (OWASP): the
// basename only (no path components), no control characters or header-breaking
// quotes, no leading dots (no dotfiles), ".." collapsed, capped length. It is
// never used to build a storage path (keys are content hashes) — this guards
// every OTHER consumer of the stored name (headers, UIs, exports). Empty in →
// empty out (a nameless upload is legitimate; serve-time falls back to
// "download").
func SanitizeName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '"' || r == '\\' || r < 0x20 || r == 0x7f:
			return -1
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return r
		case strings.ContainsRune("-_.() +,@#&=", r) || r == ' ':
			return r
		default:
			return '_'
		}
	}, name)
	for strings.Contains(name, "..") {
		name = strings.ReplaceAll(name, "..", ".")
	}
	name = strings.TrimLeft(name, ". ")
	if runes := []rune(name); len(runes) > 200 {
		name = string(runes[:200])
	}
	return name
}
