// targzpack 把暂存目录打包为 tar.gz，并显式控制文件权限位。
//
// Windows 下用 bsdtar 打包时无法可靠保留可执行位（NTFS 没有 POSIX 权限），
// 因此 Linux 发行包统一用本工具生成：
//
//	go run ./scripts/targzpack <output.tar.gz> <top-dir-name> <stage-dir>
//
// 权限规则：ds2api / mihomo / *.sh 为 0755，其余文件 0644，目录 0755。
package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func fileMode(name string) int64 {
	base := filepath.Base(name)
	if base == "ds2api" || base == "mihomo" || strings.HasSuffix(base, ".sh") {
		return 0o755
	}
	return 0o644
}

func run(outPath, topName, stageDir string) error {
	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "targzpack: close output: %v\n", closeErr)
		}
	}()

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	walkErr := filepath.WalkDir(stageDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(stageDir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		archiveName := filepath.ToSlash(filepath.Join(topName, rel))
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Name:     archiveName + "/",
				Typeflag: tar.TypeDir,
				Mode:     0o755,
			})
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if writeErr := tw.WriteHeader(&tar.Header{
			Name:     archiveName,
			Typeflag: tar.TypeReg,
			Mode:     fileMode(path),
			Size:     info.Size(),
		}); writeErr != nil {
			return writeErr
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "targzpack: close %s: %v\n", path, closeErr)
			}
		}()
		if _, copyErr := io.Copy(tw, f); copyErr != nil {
			return copyErr
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: targzpack <output.tar.gz> <top-dir-name> <stage-dir>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintf(os.Stderr, "targzpack: %v\n", err)
		os.Exit(1)
	}
}
