//go:build !windows

package main

// detachConsoleIfNeeded 在 Unix 上是 no-op。Unix 没有"Windows GUI 父进程 spawn console
// 子进程时自动分配 console"这种行为，stderr/stdout 由 parent 决定，不需要主动释放。
func detachConsoleIfNeeded() {}
