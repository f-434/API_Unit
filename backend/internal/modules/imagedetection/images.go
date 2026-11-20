package imagedetection

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

// sniffImage 通过文件头判断真实图片格式，返回格式名与推荐扩展名。
// 不依赖响应声明的 Content-Type，因为不少网关会返回错误的类型。
func sniffImage(raw []byte) (format, ext string) {
	if len(raw) < 12 {
		return "", ""
	}
	switch {
	case bytes.HasPrefix(raw, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "png", ".png"
	case bytes.HasPrefix(raw, []byte{0xFF, 0xD8, 0xFF}):
		return "jpeg", ".jpg"
	case bytes.HasPrefix(raw, []byte("GIF87a")) || bytes.HasPrefix(raw, []byte("GIF89a")):
		return "gif", ".gif"
	case bytes.HasPrefix(raw, []byte("RIFF")) && string(raw[8:12]) == "WEBP":
		return "webp", ".webp"
	}
	return "", ""
}

// normalizeFormat 把 jpg / image/jpeg 等写法统一成规范格式名。
func normalizeFormat(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "image/")
	switch s {
	case "jpg", "jpe", "jpeg":
		return "jpeg"
	case "png", "gif", "webp":
		return s
	default:
		return s
	}
}

// imageSize 读取图片宽高。PNG / JPEG / GIF / WebP 都支持，
// 不依赖完整解码，适合大图快速检查。
func imageSize(raw []byte, format string) (int, int, bool) {
	switch format {
	case "png":
		if len(raw) < 24 || string(raw[12:16]) != "IHDR" {
			return 0, 0, false
		}
		w := int(binary.BigEndian.Uint32(raw[16:20]))
		h := int(binary.BigEndian.Uint32(raw[20:24]))
		return w, h, w > 0 && h > 0
	case "jpeg":
		return jpegSize(raw)
	case "gif":
		if len(raw) < 10 {
			return 0, 0, false
		}
		w := int(binary.LittleEndian.Uint16(raw[6:8]))
		h := int(binary.LittleEndian.Uint16(raw[8:10]))
		return w, h, w > 0 && h > 0
	case "webp":
		return webpSize(raw)
	}
	return 0, 0, false
}

func jpegSize(raw []byte) (int, int, bool) {
	i := 2
	for i+9 < len(raw) {
		if raw[i] != 0xFF {
			i++
			continue
		}
		marker := raw[i+1]
		// 无长度的标记。
		if marker == 0xD8 || marker == 0xD9 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		if i+4 > len(raw) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(raw[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(raw) {
			break
		}
		// SOF0-SOF15（排除 DHT / JPG / DAC 等非 SOF 标记）。
		isSOF := (marker >= 0xC0 && marker <= 0xC3) ||
			(marker >= 0xC5 && marker <= 0xC7) ||
			(marker >= 0xC9 && marker <= 0xCB) ||
			(marker >= 0xCD && marker <= 0xCF)
		if isSOF {
			if i+9 > len(raw) {
				break
			}
			h := int(binary.BigEndian.Uint16(raw[i+5 : i+7]))
			w := int(binary.BigEndian.Uint16(raw[i+7 : i+9]))
			return w, h, w > 0 && h > 0
		}
		i += 2 + segLen
	}
	return 0, 0, false
}

func webpSize(raw []byte) (int, int, bool) {
	if len(raw) < 25 {
		return 0, 0, false
	}
	switch string(raw[12:16]) {
	case "VP8X":
		// VP8X: 4 字节 chunk 头 + 1 字节 flags + 3 字节保留，
		// 画布宽高各占 3 字节（值 = 实际尺寸 - 1）。
		if len(raw) < 30 {
			return 0, 0, false
		}
		w := int(uint32(raw[24])|uint32(raw[25])<<8|uint32(raw[26])<<16) + 1
		h := int(uint32(raw[27])|uint32(raw[28])<<8|uint32(raw[29])<<16) + 1
		return w, h, w > 0 && h > 0
	case "VP8 ":
		if len(raw) < 30 || raw[23] != 0x9D || raw[24] != 0x01 || raw[25] != 0x2A {
			return 0, 0, false
		}
		w := int(binary.LittleEndian.Uint16(raw[26:28]) & 0x3FFF)
		h := int(binary.LittleEndian.Uint16(raw[28:30]) & 0x3FFF)
		return w, h, w > 0 && h > 0
	case "VP8L":
		if len(raw) < 25 || raw[20] != 0x2F {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(raw[21:25])
		w := 1 + int(bits&0x3FFF)
		h := 1 + int((bits>>14)&0x3FFF)
		return w, h, true
	}
	return 0, 0, false
}

// hasTransparentPixels 扫描像素判定是否存在透明像素。
// 第二个返回值表示是否可以精确判断；WebP 没有内置解码器时返回 false。
func hasTransparentPixels(raw []byte, format string) (bool, bool) {
	if format == "jpeg" {
		return false, true // JPEG 不支持 alpha。
	}
	if format == "webp" {
		// 只能根据 VP8X/VP8L 的 alpha 能力位做保守判断。
		if alpha, ok := webpAlphaCapable(raw); ok {
			return alpha, false
		}
		return false, false
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return false, false
	}
	b := img.Bounds()
	// 常见 RGBA/NRGBA 走 Pix 快路径。
	switch m := img.(type) {
	case *image.NRGBA:
		for i := 3; i < len(m.Pix); i += 4 {
			if m.Pix[i] < 255 {
				return true, true
			}
		}
		return false, true
	case *image.RGBA:
		for i := 3; i < len(m.Pix); i += 4 {
			if m.Pix[i] < 255 {
				return true, true
			}
		}
		return false, true
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a < 0xFFFF {
				return true, true
			}
		}
	}
	return false, true
}

func webpAlphaCapable(raw []byte) (bool, bool) {
	if len(raw) < 25 {
		return false, false
	}
	switch string(raw[12:16]) {
	case "VP8X":
		return raw[20]&0x10 != 0, true
	case "VP8L":
		return true, true // 无损格式始终携带 alpha 通道。
	}
	return false, false
}

// checkImage 返回格式、尺寸和参数一致性检查结果。
func checkImage(raw []byte, expectedSize, expectedFormat, background string) (string, int, int, []CheckDetail) {
	format, _ := sniffImage(raw)
	checks := make([]CheckDetail, 0, 5)
	if format == "" {
		checks = append(checks, CheckDetail{Name: "图片格式", Status: StatusFail, Detail: "无法识别的图片格式（响应可能不是图片）"})
		return "", 0, 0, checks
	}
	checks = append(checks, CheckDetail{Name: "图片格式", Status: StatusPass, Detail: "实际 " + format})

	w, h, ok := imageSize(raw, format)
	if !ok {
		checks = append(checks, CheckDetail{Name: "尺寸", Status: StatusFail, Detail: "无法解析图片宽高"})
	} else if strings.TrimSpace(expectedSize) == "" {
		checks = append(checks, CheckDetail{Name: "尺寸", Status: StatusNA, Detail: fmt.Sprintf("未指定期望尺寸，实际 %dx%d", w, h)})
	} else if ew, eh, err := parseSize(expectedSize); err != nil {
		checks = append(checks, CheckDetail{Name: "尺寸", Status: StatusFail, Detail: "期望尺寸解析失败: " + err.Error()})
	} else if w != ew || h != eh {
		checks = append(checks, CheckDetail{Name: "尺寸", Status: StatusFail, Detail: fmt.Sprintf("期望 %dx%d，实际 %dx%d", ew, eh, w, h)})
	} else {
		checks = append(checks, CheckDetail{Name: "尺寸", Status: StatusPass, Detail: fmt.Sprintf("期望 %dx%d，实际 %dx%d", ew, eh, w, h)})
	}

	expect := normalizeFormat(expectedFormat)
	if expect == "" {
		checks = append(checks, CheckDetail{Name: "输出格式", Status: StatusNA, Detail: "未指定期望格式，实际 " + format})
	} else if normalizeFormat(format) != expect {
		checks = append(checks, CheckDetail{Name: "输出格式", Status: StatusFail, Detail: fmt.Sprintf("期望 %s，实际 %s", expect, format)})
	} else {
		checks = append(checks, CheckDetail{Name: "输出格式", Status: StatusPass, Detail: fmt.Sprintf("期望 %s，实际 %s", expect, format)})
	}

	hasAlpha, exact := hasTransparentPixels(raw, format)
	switch background {
	case "transparent":
		switch {
		case !exact:
			checks = append(checks, CheckDetail{Name: "透明背景", Status: StatusWarn, Detail: fmt.Sprintf("请求 transparent；%s 无法逐像素确认，容器 alpha 能力=%v", format, hasAlpha)})
		case hasAlpha:
			checks = append(checks, CheckDetail{Name: "透明背景", Status: StatusPass, Detail: "请求 transparent，检测到透明像素"})
		default:
			checks = append(checks, CheckDetail{Name: "透明背景", Status: StatusFail, Detail: "请求 transparent，但图片完全不透明"})
		}
	case "opaque":
		switch {
		case !exact:
			checks = append(checks, CheckDetail{Name: "透明背景", Status: StatusWarn, Detail: fmt.Sprintf("请求 opaque；%s 无法逐像素确认，容器 alpha 能力=%v", format, hasAlpha)})
		case hasAlpha:
			checks = append(checks, CheckDetail{Name: "透明背景", Status: StatusFail, Detail: "请求 opaque，但图片存在透明像素"})
		default:
			checks = append(checks, CheckDetail{Name: "透明背景", Status: StatusPass, Detail: "请求 opaque，图片完全不透明"})
		}
	case "auto":
		checks = append(checks, CheckDetail{Name: "透明背景", Status: StatusPass, Detail: fmt.Sprintf("请求 auto，存在透明像素=%v", hasAlpha)})
	default:
		checks = append(checks, CheckDetail{Name: "透明背景", Status: StatusNA, Detail: "未指定 background"})
	}
	return format, w, h, checks
}

func parseSize(size string) (int, int, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("无效尺寸: %s", size)
	}
	var w, h int
	if _, err := fmt.Sscanf(parts[0], "%d", &w); err != nil || w <= 0 {
		return 0, 0, fmt.Errorf("无效尺寸: %s", size)
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &h); err != nil || h <= 0 {
		return 0, 0, fmt.Errorf("无效尺寸: %s", size)
	}
	return w, h, nil
}

// detectSize 按模型名推断目标尺寸，与 image-param-verifier 保持一致。
func detectSize(name string) string {
	m := strings.ToLower(name)
	switch {
	case strings.Contains(m, "4k"):
		return "4096x4096"
	case strings.Contains(m, "2k"):
		return "2048x2048"
	default:
		return "1024x1024"
	}
}
