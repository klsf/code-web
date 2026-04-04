package main

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
)

const uploadsDir = "data/uploads"

// ensureUploadsDir 确保图片上传目录存在。
func ensureUploadsDir() error {
	return os.MkdirAll(uploadsDir, 0o755)
}

// saveUploadedFile 保存一张上传图片，并返回最终文件名。
func saveUploadedFile(file multipart.File, header *multipart.FileHeader) (string, error) {
	ext := strings.ToLower(filepath.Ext(header.Filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
	default:
		return "", errors.New("仅支持 jpg、jpeg、png、gif、webp 图片")
	}

	filename := newUUID() + ext
	path := filepath.Join(uploadsDir, filename)
	dst, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		return "", err
	}
	return filename, nil
}

// resolveImageFiles 把图片文件名转换为前端 URL 和本地绝对路径。
func resolveImageFiles(imageIDs []string) ([]string, []string, error) {
	urls := make([]string, 0, len(imageIDs))
	paths := make([]string, 0, len(imageIDs))
	for _, id := range imageIDs {
		name := filepath.Base(strings.TrimSpace(id))
		if name == "" || name == "." {
			continue
		}
		path := filepath.Join(uploadsDir, name)
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, nil, err
		}
		if _, err := os.Stat(absPath); err != nil {
			return nil, nil, fmt.Errorf("图片不存在: %s", name)
		}
		urls = append(urls, "/uploads/"+name)
		paths = append(paths, absPath)
	}
	return urls, paths, nil
}
