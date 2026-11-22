//go:build ignore

// Pure-Go generator for Aronium Utility Windows application icon (.ico) and
// COFF resource object files (rsrc_windows_amd64.syso & rsrc_windows_386.syso)
// embedding RT_ICON, RT_GROUP_ICON, RT_VERSION (VS_VERSION_INFO), and
// RT_MANIFEST (asInvoker + Windows 10/11 compatibility).
//
// Zero external tools or CGO required — produces deterministic PE resources on
// macOS, Linux, and Windows to improve Windows shell integration and reduce
// SmartScreen/antivirus heuristic false positives.

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

const (
	appVersionMajor = 1
	appVersionMinor = 2
	appVersionPatch = 3
	appVersionBuild = 0
	appVersionStr   = "1.2.3.0"
)

type targetBinary struct {
	Dir              string
	FileDescription  string
	InternalName     string
	OriginalFilename string
	ProductName      string
}

var targets = []targetBinary{
	{
		Dir:              "cmd/aronium-util",
		FileDescription:  "Aronium Utility CLI Engine (POS Read-Only / Cuentas Ledger / Drive Sync)",
		InternalName:     "aronium-util.exe",
		OriginalFilename: "aronium-util.exe",
		ProductName:      "Aronium Utility CLI",
	},
	{
		Dir:              "cmd/aronium-utility-ui",
		FileDescription:  "Aronium Utility UI (Cuentas de Credito, Escaner HID e Inventario Neto)",
		InternalName:     "aronium-utility-ui.exe",
		OriginalFilename: "aronium-utility-ui.exe",
		ProductName:      "Aronium Utility UI",
	},
	{
		Dir:              "cmd/aronium-util-setup",
		FileDescription:  "Aronium Utility Unified Setup Installer v1.2.3 (CLI + UI)",
		InternalName:     "aronium-util-setup.exe",
		OriginalFilename: "aronium-util-setup-x64.exe",
		ProductName:      "Aronium Utility Installer",
	},
}

func main() {
	rootDir, err := os.Getwd()
	if err != nil {
		fatalf("getwd: %v", err)
	}

	sizes := []int{256, 64, 48, 32, 16}
	entries := make([]iconEntry, 0, len(sizes))
	for _, sz := range sizes {
		img := renderAppIcon(sz)
		if sz == 64 {
			verifyIconBrightness(img)
		}
		if sz == 256 {
			var pngBuf bytes.Buffer
			if err := png.Encode(&pngBuf, img); err != nil {
				fatalf("encode 256px PNG: %v", err)
			}
			entries = append(entries, iconEntry{
				width:  sz,
				height: sz,
				data:   pngBuf.Bytes(),
			})
		} else {
			entries = append(entries, iconEntry{
				width:  sz,
				height: sz,
				data:   encodeIconDIB(img),
			})
		}
	}

	icoBytes := buildICOFile(entries)
	for _, relPath := range []string{
		filepath.Join("docs", "assets", "app-icon.ico"),
		filepath.Join("cmd", "aronium-util-setup", "payload", "app-icon.ico"),
	} {
		outPath := filepath.Join(rootDir, relPath)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			fatalf("mkdir %s: %v", filepath.Dir(outPath), err)
		}
		if err := os.WriteFile(outPath, icoBytes, 0o644); err != nil {
			fatalf("write %s: %v", outPath, err)
		}
	}
	fmt.Printf("[ok] Generated multi-resolution icon: docs/assets/app-icon.ico & payload/app-icon.ico (%d bytes)\n", len(icoBytes))

	manifestBytes := buildAppManifest()
	grpIconBytes := buildGroupIconData(entries)

	const (
		machineAMD64 = 0x8664
		machineI386  = 0x014c
	)

	for _, t := range targets {
		verBytes := buildVersionInfo(t)
		rsrcSection, relocOffsets := buildRsrcSection(entries, grpIconBytes, verBytes, manifestBytes)

		targetDir := filepath.Join(rootDir, t.Dir)
		amd64Path := filepath.Join(targetDir, "rsrc_windows_amd64.syso")
		if err := os.WriteFile(amd64Path, buildCOFFSyso(machineAMD64, rsrcSection, relocOffsets), 0o644); err != nil {
			fatalf("write %s: %v", amd64Path, err)
		}

		i386Path := filepath.Join(targetDir, "rsrc_windows_386.syso")
		if err := os.WriteFile(i386Path, buildCOFFSyso(machineI386, rsrcSection, relocOffsets), 0o644); err != nil {
			fatalf("write %s: %v", i386Path, err)
		}
		fmt.Printf("[ok] Generated COFF PE resources (.syso) for %s (amd64 + 386)\n", t.Dir)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen_winres error: "+format+"\n", args...)
	os.Exit(1)
}

type iconEntry struct {
	width  int
	height int
	data   []byte
}

func verifyIconBrightness(img *image.NRGBA) {
	b := img.Bounds()
	var sum float64
	var count float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.NRGBAAt(x, y)
			if c.A >= 128 {
				sum += (float64(c.R) + float64(c.G) + float64(c.B)) / 3.0
				count++
			}
		}
	}
	if count == 0 {
		fatalf("rendered icon has no opaque pixels")
	}
	avg := sum / count
	if avg < 80 {
		fatalf("rendered icon average RGB brightness too low (%.1f < 80)", avg)
	}
	fmt.Printf("[ok] Verified 64x64 icon average brightness: %.1f/255 (%d opaque pixels)\n", avg, int(count))
}

// renderAppIcon renders the Aronium Utility badge cleanly with 4x supersampling
// so 16x16..256x256 Windows Explorer and Desktop shortcut icons look crisp and vibrant.
func renderAppIcon(size int) *image.NRGBA {
	const ss = 4
	w := size * ss
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	scale := 256.0 / float64(w)
	samples := float64(ss * ss)

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var rSum, gSum, bSum, afSum float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					vx := (float64(px*ss+sx) + 0.5) * scale
					vy := (float64(py*ss+sy) + 0.5) * scale
					c := sampleVectorIcon(vx, vy)
					af := float64(c.A) / 255.0
					rSum += float64(c.R) * af
					gSum += float64(c.G) * af
					bSum += float64(c.B) * af
					afSum += af
				}
			}
			avgA := (afSum / samples) * 255.0
			if avgA < 0.5 || afSum <= 1e-6 {
				continue
			}
			outR := uint8(clamp255(rSum / afSum))
			outG := uint8(clamp255(gSum / afSum))
			outB := uint8(clamp255(bSum / afSum))
			outA := uint8(clamp255(avgA))
			img.SetNRGBA(px, py, color.NRGBA{R: outR, G: outG, B: outB, A: outA})
		}
	}
	return img
}

func sampleVectorIcon(x, y float64) color.NRGBA {
	// Outer rounded rect: x,y in [8, 248], radius 54
	dOuter := sdRoundedBox(x-128, y-128, 120, 120, 54)
	if dOuter > 0 {
		return color.NRGBA{0, 0, 0, 0}
	}

	// Vibrant diagonal gradient: royal blue (#1d4ed8) -> bright sky (#0284c7) -> vivid teal (#0d9488)
	t := clamp01((x + y) / 512.0)
	var bg color.NRGBA
	if t < 0.5 {
		bg = lerpColor(color.NRGBA{0x1d, 0x4e, 0xd8, 255}, color.NRGBA{0x02, 0x84, 0xc7, 255}, t/0.5)
	} else {
		bg = lerpColor(color.NRGBA{0x02, 0x84, 0xc7, 255}, color.NRGBA{0x0d, 0x94, 0x88, 255}, (t-0.5)/0.5)
	}

	// Outer crisp light-cyan border (#bae6fd, ~9px stroke for high contrast on dark/light Windows themes)
	if dOuter > -9.0 {
		return color.NRGBA{0xba, 0xe6, 0xfd, 255}
	}

	// Left: Bright SQLite cylinder motif centered near (80, 110)
	if x >= 38 && x <= 122 && y >= 62 && y <= 156 {
		inTopEllipse := math.Pow((x-80)/42.0, 2)+math.Pow((y-78)/16.0, 2) <= 1.0
		inBotEllipse := math.Pow((x-80)/42.0, 2)+math.Pow((y-140)/16.0, 2) <= 1.0
		inBody := x >= 38 && x <= 122 && y >= 78 && y <= 140
		if inTopEllipse || inBody || inBotEllipse {
			topRim := math.Abs(math.Sqrt(math.Pow((x-80)/42.0, 2)+math.Pow((y-78)/16.0, 2))-1.0) < 0.16
			midBand := math.Abs(y-108) < 4.5 && x >= 42 && x <= 118
			sideEdge := (x < 44 || x > 116) && y >= 78 && y <= 140
			botRim := y > 136 && math.Abs(math.Sqrt(math.Pow((x-80)/42.0, 2)+math.Pow((y-140)/16.0, 2))-1.0) < 0.16
			if topRim || sideEdge || botRim {
				return color.NRGBA{0xff, 0xff, 0xff, 255}
			}
			if midBand {
				return color.NRGBA{0xe0, 0xf2, 0xfe, 255}
			}
			if inTopEllipse {
				return color.NRGBA{0xba, 0xe6, 0xfd, 255}
			}
			return color.NRGBA{0x0e, 0xa5, 0xe9, 255}
		}
	}

	// Right: Vivid Emerald Ledger Card centered at (182, 108), half-size (36, 48), radius 10
	dCard := sdRoundedBox(x-182, y-108, 36, 48, 10)
	if dCard <= 0 {
		if dCard > -5.0 {
			return color.NRGBA{0xec, 0xfd, 0xf5, 255}
		}
		if x >= 156 && x <= 208 {
			if math.Abs(y-82) < 4.2 || math.Abs(y-104) < 4.2 || (math.Abs(y-126) < 4.2 && x <= 196) {
				return color.NRGBA{0xff, 0xff, 0xff, 255}
			}
		}
		return color.NRGBA{0x10, 0xb9, 0x81, 255}
	}

	// Center Sync Arrow: bright yellow-gold -> crisp white
	if x >= 120 && x <= 162 && y >= 90 && y <= 128 {
		onShaft := math.Abs(y-109) <= 5.0 && x >= 122 && x <= 156
		onHead := x >= 140 && x <= 159 && math.Abs(y-109) <= (159-x)*1.1
		if onShaft || onHead {
			return lerpColor(color.NRGBA{0xfd, 0xe0, 0x47, 255}, color.NRGBA{0xff, 0xff, 0xff, 255}, clamp01((x-122)/36.0))
		}
	}

	// Bottom badge bar at (128, 202), half-size (86, 20), radius 10
	dPill := sdRoundedBox(x-128, y-202, 86, 20, 10)
	if dPill <= 0 {
		if dPill > -4.0 {
			return color.NRGBA{0x7d, 0xd3, 0xfc, 255}
		}
		dBar := sdRoundedBox(x-128, y-202, 58, 5.5, 2.5)
		if dBar <= 0 {
			if x < 140 {
				return color.NRGBA{0x34, 0xd3, 0x99, 255}
			}
			return color.NRGBA{0x38, 0xbd, 0xf8, 255}
		}
		return color.NRGBA{0x1e, 0x3a, 0x8a, 255}
	}

	return bg
}

func sdRoundedBox(px, py, bx, by, r float64) float64 {
	qx := math.Abs(px) - bx + r
	qy := math.Abs(py) - by + r
	dx := math.Max(qx, 0)
	dy := math.Max(qy, 0)
	return math.Min(math.Max(qx, qy), 0) + math.Sqrt(dx*dx+dy*dy) - r
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clamp255(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return math.Round(v)
}

func lerpColor(a, b color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{
		R: uint8(clamp255(float64(a.R) + (float64(b.R)-float64(a.R))*t)),
		G: uint8(clamp255(float64(a.G) + (float64(b.G)-float64(a.G))*t)),
		B: uint8(clamp255(float64(a.B) + (float64(b.B)-float64(a.B))*t)),
		A: uint8(clamp255(float64(a.A) + (float64(b.A)-float64(a.A))*t)),
	}
}

// encodeIconDIB produces a 40-byte BITMAPINFOHEADER + bottom-up 32bpp BGRA + 1bpp AND mask.
func encodeIconDIB(img *image.NRGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	andStride := ((w + 31) / 32) * 4

	var buf bytes.Buffer
	// BITMAPINFOHEADER (40 bytes)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(40))        // biSize
	_ = binary.Write(&buf, binary.LittleEndian, int32(w))          // biWidth
	_ = binary.Write(&buf, binary.LittleEndian, int32(h*2))        // biHeight (XOR + AND masks)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))         // biPlanes
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32))        // biBitCount
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))         // biCompression (BI_RGB)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(w*h*4))     // biSizeImage
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))          // biXPelsPerMeter
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))          // biYPelsPerMeter
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))         // biClrUsed
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))         // biClrImportant

	// Bottom-up BGRA pixels
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			buf.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}

	// Bottom-up 1-bit AND mask (0 = opaque pixel, 1 = fully transparent pixel)
	andRow := make([]byte, andStride)
	for y := h - 1; y >= 0; y-- {
		for i := range andRow {
			andRow[i] = 0
		}
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			if c.A == 0 {
				andRow[x/8] |= 1 << uint(7-(x%8))
			}
		}
		buf.Write(andRow)
	}
	return buf.Bytes()
}

func icoDimByte(dim int) uint8 {
	if dim >= 256 {
		return 0
	}
	return uint8(dim)
}

func buildICOFile(entries []iconEntry) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0))            // idReserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))            // idType (1 = ICO)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(entries))) // idCount

	offset := uint32(6 + len(entries)*16)
	for _, e := range entries {
		buf.WriteByte(icoDimByte(e.width))
		buf.WriteByte(icoDimByte(e.height))
		buf.WriteByte(0) // bColorCount
		buf.WriteByte(0) // bReserved
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))            // wPlanes
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32))           // wBitCount
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(e.data)))  // dwBytesInRes
		_ = binary.Write(&buf, binary.LittleEndian, offset)               // dwImageOffset
		offset += uint32(len(e.data))
	}
	for _, e := range entries {
		buf.Write(e.data)
	}
	return buf.Bytes()
}

func buildGroupIconData(entries []iconEntry) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0))            // idReserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))            // idType (1 = ICO)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(entries))) // idCount

	for idx, e := range entries {
		buf.WriteByte(icoDimByte(e.width))
		buf.WriteByte(icoDimByte(e.height))
		buf.WriteByte(0) // bColorCount
		buf.WriteByte(0) // bReserved
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))           // wPlanes
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32))          // wBitCount
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(e.data))) // dwBytesInRes
		_ = binary.Write(&buf, binary.LittleEndian, uint16(idx+1))       // nID referencing RT_ICON ID
	}
	return buf.Bytes()
}

func buildAppManifest() []byte {
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity version="` + appVersionStr + `" processorArchitecture="*" name="SilverFeit.AroniumUtility" type="win32"/>
  <description>Aronium Utility - Local POS Ledger &amp; Google Drive Sync</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <!-- Windows 10 and Windows 11 -->
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
      <!-- Windows 8.1 -->
      <supportedOS Id="{1f676c76-80e1-4239-95bb-83d0f6d0da78}"/>
      <!-- Windows 8 -->
      <supportedOS Id="{4a2f28e3-53b9-4441-ba9c-d69d4a4a6e38}"/>
      <!-- Windows 7 -->
      <supportedOS Id="{35138b9a-5d96-4fbd-8e2d-a2440225f93a}"/>
    </application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2, system</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
    </windowsSettings>
  </application>
</assembly>
`
	return []byte(strings.ReplaceAll(xml, "\n", "\r\n"))
}

func encodeUTF16Null(s string) []byte {
	units := utf16.Encode([]rune(s + "\x00"))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[i*2:], u)
	}
	return out
}

func padTo4(buf *bytes.Buffer) {
	for buf.Len()%4 != 0 {
		buf.WriteByte(0)
	}
}

// buildVersionNode builds a VS_VERSION_INFO tree node.
// When isTextValue is true, wValueLength stores UTF-16 code units (WORDS); otherwise BYTES.
func buildVersionNode(key string, isTextValue bool, value []byte, children ...[]byte) []byte {
	var body bytes.Buffer
	wType := uint16(0)
	wValueLength := uint16(len(value))
	if isTextValue {
		wType = 1
		wValueLength = uint16(len(value) / 2)
	}

	// Placeholder for wLength (filled after building full node)
	_ = binary.Write(&body, binary.LittleEndian, uint16(0))
	_ = binary.Write(&body, binary.LittleEndian, wValueLength)
	_ = binary.Write(&body, binary.LittleEndian, wType)
	body.Write(encodeUTF16Null(key))
	padTo4(&body)

	if len(value) > 0 {
		body.Write(value)
		padTo4(&body)
	}

	for i, child := range children {
		padTo4(&body)
		body.Write(child)
		if i < len(children)-1 {
			padTo4(&body)
		}
	}

	raw := body.Bytes()
	binary.LittleEndian.PutUint16(raw[0:2], uint16(len(raw)))
	return raw
}

func buildVersionInfo(t targetBinary) []byte {
	// VS_FIXEDFILEINFO (52 bytes)
	var fixed bytes.Buffer
	verMS := uint32((appVersionMajor << 16) | appVersionMinor)
	verLS := uint32((appVersionPatch << 16) | appVersionBuild)
	_ = binary.Write(&fixed, binary.LittleEndian, uint32(0xFEEF04BD)) // dwSignature
	_ = binary.Write(&fixed, binary.LittleEndian, uint32(0x00010000)) // dwStrucVersion
	_ = binary.Write(&fixed, binary.LittleEndian, verMS)              // dwFileVersionMS
	_ = binary.Write(&fixed, binary.LittleEndian, verLS)              // dwFileVersionLS
	_ = binary.Write(&fixed, binary.LittleEndian, verMS)              // dwProductVersionMS
	_ = binary.Write(&fixed, binary.LittleEndian, verLS)              // dwProductVersionLS
	_ = binary.Write(&fixed, binary.LittleEndian, uint32(0x3F))       // dwFileFlagsMask
	_ = binary.Write(&fixed, binary.LittleEndian, uint32(0))          // dwFileFlags
	_ = binary.Write(&fixed, binary.LittleEndian, uint32(0x00040004)) // dwFileOS (VOS_NT_WINDOWS32)
	_ = binary.Write(&fixed, binary.LittleEndian, uint32(0x00000001)) // dwFileType (VFT_APP)
	_ = binary.Write(&fixed, binary.LittleEndian, uint32(0))          // dwFileSubtype
	_ = binary.Write(&fixed, binary.LittleEndian, uint32(0))          // dwFileDateMS
	_ = binary.Write(&fixed, binary.LittleEndian, uint32(0))          // dwFileDateLS

	kvPairs := [][2]string{
		{"CompanyName", "SilverFeit"},
		{"FileDescription", t.FileDescription},
		{"FileVersion", appVersionStr},
		{"InternalName", t.InternalName},
		{"LegalCopyright", "Copyright (c) 2026 SilverFeit (MIT License)"},
		{"OriginalFilename", t.OriginalFilename},
		{"ProductName", t.ProductName},
		{"ProductVersion", appVersionStr},
	}
	stringNodes := make([][]byte, 0, len(kvPairs))
	for _, kv := range kvPairs {
		stringNodes = append(stringNodes, buildVersionNode(kv[0], true, encodeUTF16Null(kv[1])))
	}
	stringTable := buildVersionNode("040904b0", true, nil, stringNodes...)
	stringFileInfo := buildVersionNode("StringFileInfo", true, nil, stringTable)

	var transBuf bytes.Buffer
	_ = binary.Write(&transBuf, binary.LittleEndian, uint16(0x0409)) // English (US)
	_ = binary.Write(&transBuf, binary.LittleEndian, uint16(0x04b0)) // Unicode UTF-16LE
	varEntry := buildVersionNode("Translation", false, transBuf.Bytes())
	varFileInfo := buildVersionNode("VarFileInfo", true, nil, varEntry)

	return buildVersionNode("VS_VERSION_INFO", false, fixed.Bytes(), stringFileInfo, varFileInfo)
}

type rsrcItem struct {
	typeID uint32
	nameID uint32
	langID uint32
	data   []byte
}

// buildRsrcSection constructs a sorted 3-level Windows PE .rsrc section and returns
// the section payload alongside section-relative offsets of every IMAGE_RESOURCE_DATA_ENTRY.OffsetToData
// field requiring a COFF relocation.
func buildRsrcSection(icons []iconEntry, grpIcon, versionInfo, manifest []byte) ([]byte, []uint32) {
	items := make([]rsrcItem, 0, len(icons)+3)
	// Sorted by (typeID, nameID, langID): 3 (RT_ICON), 14 (RT_GROUP_ICON), 16 (RT_VERSION), 24 (RT_MANIFEST)
	for idx, ic := range icons {
		items = append(items, rsrcItem{typeID: 3, nameID: uint32(idx + 1), langID: 0x0409, data: ic.data})
	}
	items = append(items,
		rsrcItem{typeID: 14, nameID: 1, langID: 0x0409, data: grpIcon},
		rsrcItem{typeID: 16, nameID: 1, langID: 0x0409, data: versionInfo},
		rsrcItem{typeID: 24, nameID: 1, langID: 0x0409, data: manifest},
	)

	// Group by typeID (order already ascending: 3, 14, 16, 24)
	type typeGroup struct {
		typeID uint32
		items  []rsrcItem
	}
	var groups []typeGroup
	for _, it := range items {
		if len(groups) == 0 || groups[len(groups)-1].typeID != it.typeID {
			groups = append(groups, typeGroup{typeID: it.typeID})
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, it)
	}

	// Calculate directory layout sizes carefully:
	// Level 1 (Root Directory): 16 + len(groups)*8
	rootDirSize := 16 + len(groups)*8

	// Level 2 (One Type Dir per distinct typeID):
	// Each group has distinct nameIDs (1..N for RT_ICON, 1 for others)
	level2Offset := rootDirSize
	typeDirOffsets := make([]int, len(groups))
	level2Size := 0
	for gi, g := range groups {
		typeDirOffsets[gi] = level2Offset + level2Size
		level2Size += 16 + len(g.items)*8
	}

	// Level 3 (One Name Dir per item, each containing 1 langID):
	level3Offset := level2Offset + level2Size
	nameDirOffsets := make([]int, len(items))
	level3Size := 0
	for ii := range items {
		nameDirOffsets[ii] = level3Offset + level3Size
		level3Size += 16 + 1*8
	}

	// Data Entries (one 16-byte IMAGE_RESOURCE_DATA_ENTRY per item):
	dataEntriesOffset := level3Offset + level3Size
	dataEntriesSize := len(items) * 16

	// Raw resource payloads (4-byte aligned each):
	rawOffset := dataEntriesOffset + dataEntriesSize
	rawOffsets := make([]int, len(items))
	curRaw := rawOffset
	for ii, it := range items {
		rawOffsets[ii] = curRaw
		curRaw += (len(it.data) + 3) &^ 3
	}
	totalSize := (curRaw + 7) &^ 7

	sec := make([]byte, totalSize)
	relocOffsets := make([]uint32, 0, len(items))

	writeDirHeader := func(off int, idEntries int) {
		binary.LittleEndian.PutUint16(sec[off+12:], 0)
		binary.LittleEndian.PutUint16(sec[off+14:], uint16(idEntries))
	}
	writeDirEntry := func(off int, id uint32, target uint32, isSubdir bool) {
		binary.LittleEndian.PutUint32(sec[off:], id)
		if isSubdir {
			target |= 0x80000000
		}
		binary.LittleEndian.PutUint32(sec[off+4:], target)
	}

	// Write Level 1 Root Dir
	writeDirHeader(0, len(groups))
	itemCursor := 0
	for gi, g := range groups {
		writeDirEntry(16+gi*8, g.typeID, uint32(typeDirOffsets[gi]), true)
		// Write Level 2 Type Dir
		tdOff := typeDirOffsets[gi]
		writeDirHeader(tdOff, len(g.items))
		for ni, it := range g.items {
			writeDirEntry(tdOff+16+ni*8, it.nameID, uint32(nameDirOffsets[itemCursor]), true)
			// Write Level 3 Name Dir
			ndOff := nameDirOffsets[itemCursor]
			writeDirHeader(ndOff, 1)
			dataEntryOff := dataEntriesOffset + itemCursor*16
			writeDirEntry(ndOff+16, it.langID, uint32(dataEntryOff), false)

			// Write IMAGE_RESOURCE_DATA_ENTRY
			binary.LittleEndian.PutUint32(sec[dataEntryOff:], uint32(rawOffsets[itemCursor]))
			binary.LittleEndian.PutUint32(sec[dataEntryOff+4:], uint32(len(it.data)))
			binary.LittleEndian.PutUint32(sec[dataEntryOff+8:], 0) // CodePage
			binary.LittleEndian.PutUint32(sec[dataEntryOff+12:], 0)
			relocOffsets = append(relocOffsets, uint32(dataEntryOff))

			// Copy raw resource payload
			copy(sec[rawOffsets[itemCursor]:], it.data)
			itemCursor++
		}
	}

	return sec, relocOffsets
}

// buildCOFFSyso wraps a .rsrc section + ADDR32NB/DIR32NB relocations into a COFF object file (.syso).
func buildCOFFSyso(machine uint16, rsrcSection []byte, relocOffsets []uint32) []byte {
	const (
		imageFileMachineAMD64 = 0x8664
		imageRelAMD64Addr32NB = 0x0003
		imageRelI386Dir32NB   = 0x0007
		imageScnCntInitData   = 0x00000040
		imageScnMemRead       = 0x40000000
		imageSymClassStatic   = 3
	)

	relocType := uint16(imageRelI386Dir32NB)
	if machine == imageFileMachineAMD64 {
		relocType = imageRelAMD64Addr32NB
	}

	const (
		fileHeaderSize    = 20
		sectionHeaderSize = 40
	)
	ptrToRawData := uint32(fileHeaderSize + sectionHeaderSize)
	ptrToRelocs := ptrToRawData + uint32(len(rsrcSection))
	ptrToSymTable := ptrToRelocs + uint32(len(relocOffsets)*10)

	var out bytes.Buffer

	// IMAGE_FILE_HEADER (20 bytes)
	_ = binary.Write(&out, binary.LittleEndian, machine)
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))             // NumberOfSections
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))             // TimeDateStamp
	_ = binary.Write(&out, binary.LittleEndian, ptrToSymTable)         // PointerToSymbolTable
	_ = binary.Write(&out, binary.LittleEndian, uint32(1))             // NumberOfSymbols
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))             // SizeOfOptionalHeader
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))             // Characteristics

	// IMAGE_SECTION_HEADER ".rsrc" (40 bytes)
	var name [8]byte
	copy(name[:], ".rsrc")
	out.Write(name[:])
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))                                    // VirtualSize
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))                                    // VirtualAddress
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(rsrcSection)))                     // SizeOfRawData
	_ = binary.Write(&out, binary.LittleEndian, ptrToRawData)                                 // PointerToRawData
	_ = binary.Write(&out, binary.LittleEndian, ptrToRelocs)                                  // PointerToRelocations
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))                                    // PointerToLinenumbers
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(relocOffsets)))                    // NumberOfRelocations
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))                                    // NumberOfLinenumbers
	_ = binary.Write(&out, binary.LittleEndian, uint32(imageScnCntInitData|imageScnMemRead))  // Characteristics

	// Raw .rsrc section bytes
	out.Write(rsrcSection)

	// COFF Relocations (10 bytes each) referencing SymbolTableIndex 0 (".rsrc")
	for _, rOff := range relocOffsets {
		_ = binary.Write(&out, binary.LittleEndian, rOff)      // VirtualAddress (section offset)
		_ = binary.Write(&out, binary.LittleEndian, uint32(0)) // SymbolTableIndex (0 = ".rsrc")
		_ = binary.Write(&out, binary.LittleEndian, relocType) // Type (ADDR32NB / DIR32NB)
	}

	// COFF Symbol Table (18 bytes): static symbol ".rsrc" in section 1
	out.Write(name[:])
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))                 // Value
	_ = binary.Write(&out, binary.LittleEndian, int16(1))                  // SectionNumber (1-based)
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))                 // Type
	out.WriteByte(imageSymClassStatic)                                     // StorageClass
	out.WriteByte(0)                                                       // NumberOfAuxSymbols

	// Empty COFF String Table (4-byte length header = 4)
	_ = binary.Write(&out, binary.LittleEndian, uint32(4))

	return out.Bytes()
}
