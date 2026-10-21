//go:build windows

package downloader

import (
	"syscall"
	"unsafe"
)

// GetDiskStats calculates the disk usage of the volume containing targetDir on Windows
func GetDiskStats(targetDir string) DiskStats {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")

	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes uint64
	pathPtr, err := syscall.UTF16PtrFromString(targetDir)
	if err != nil {
		return DiskStats{
			TotalFormatted: "N/A",
			FreeFormatted:  "N/A",
			UsedFormatted:  "N/A",
		}
	}

	ret, _, _ := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if ret == 0 {
		return DiskStats{
			TotalFormatted: "N/A",
			FreeFormatted:  "N/A",
			UsedFormatted:  "N/A",
		}
	}

	total := totalNumberOfBytes
	free := totalNumberOfFreeBytes
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
