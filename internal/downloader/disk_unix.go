//go:build !windows

package downloader

import "syscall"

// GetDiskStats calculates the disk usage of the volume containing targetDir on Unix/Linux/macOS
func GetDiskStats(targetDir string) DiskStats {
	var stat syscall.Statfs_t
	err := syscall.Statfs(targetDir, &stat)
	if err != nil {
		return DiskStats{
			TotalFormatted: "N/A",
			FreeFormatted:  "N/A",
			UsedFormatted:  "N/A",
		}
	}

	bsize := uint64(stat.Bsize)
	total := stat.Blocks * bsize
	free := stat.Bavail * bsize
	used := total - free

	var usedPercent float64
	if total > 0 {
		usedPercent = (float64(used) / float64(total)) * 100
	}

	return DiskStats{
		TotalBytes:     total,
		FreeBytes:      free,
		UsedBytes:      used,
		UsedPercent:    usedPercent,
		TotalFormatted: FormatBytesUint64(total),
		FreeFormatted:  FormatBytesUint64(free),
		UsedFormatted:  FormatBytesUint64(used),
	}
}
