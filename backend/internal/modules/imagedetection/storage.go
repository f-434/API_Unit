package imagedetection

import (
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var (
	runIDPattern = regexp.MustCompile(`^r_[0-9a-z]+$`)
	// 文件名由随机前缀 + 阶段名 + 扩展名组成，不接受任何路径分隔符。
	imageNamePattern = regexp.MustCompile(`^[0-9a-f]{8,}_[A-Za-z0-9_-]+\.(png|jpg|jpeg|gif|webp|json)$`)
)

// ImageDir 返回生图检测的图片根目录，与 API 层保持一致。
func ImageDir(dataDir string) string {
	return filepath.Join(dataDir, "image-detection")
}

// RunImagesDir 返回某次运行独占的图片目录。
func RunImagesDir(root, runID string) string {
	return filepath.Join(root, runID)
}

// ValidRunID 只接受引擎生成的运行 ID，避免拼接出任意路径。
func ValidRunID(id string) bool { return runIDPattern.MatchString(id) }

// ValidImageFile 校验图片/请求响应文件名，防止路径穿越。
func ValidImageFile(name string) bool { return imageNamePattern.MatchString(name) }

// RemoveRunImages 在删除测试记录时清理图片，避免磁盘长期累积。
func RemoveRunImages(root, runID string) error {
	if !ValidRunID(runID) {
		return nil
	}
	return os.RemoveAll(RunImagesDir(root, runID))
}

// PurgeImages 按目录修改时间清理过期运行的图片。
func PurgeImages(root string, cutoffMs int64) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	cutoff := time.UnixMilli(cutoffMs)
	for _, e := range entries {
		if !e.IsDir() || !ValidRunID(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.RemoveAll(filepath.Join(root, e.Name())); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}
