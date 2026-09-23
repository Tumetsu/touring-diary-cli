// Package convert turns source media into web-ready files: HEIC/JPEG/PNG/WebP
// to resized JPEG plus thumbnail, and video to H.264 MP4 plus a poster frame.
// Work runs in a worker pool and is cached across builds (spec section 4.5).
package convert
