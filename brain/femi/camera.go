package femi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"

	"femi/brain/photo"
)

// The device's polaroid: 112x84 RGB565, 4:3 like the camera.
const snapW, snapH = 112, 84

// LookAtCamera takes a photo, puts a small copy on the device, and returns
// the photo for Claude to look at. Nothing is saved.
func (b *Brain) LookAtCamera(ctx context.Context) (string, []byte, string, error) {
	if b.cfg.Camera == nil {
		return "", nil, "", errors.New("Famyy has no camera connected right now")
	}
	jpg, err := b.cfg.Camera.Snap(ctx)
	if err != nil {
		b.logf("camera: %v", err)
		return "", nil, "", errors.New("the camera didn't take a picture; ask them to try again in a moment")
	}
	img, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		return "", nil, "", fmt.Errorf("the camera's picture was unreadable: %v", err)
	}
	b.mu.Lock()
	b.lastPhoto = b.cfg.Now()
	b.mu.Unlock()
	size := img.Bounds().Size()
	b.logf("camera: %dx%d photo, %d KB (not saved)", size.X, size.Y, len(jpg)/1024)
	if b.cfg.Device.Connected() { // the polaroid is up while Claude looks
		thumb := photo.RGB565(photo.Fit(photo.ToRGBA(img), snapW, snapH))
		if err := b.cfg.Device.Snapshot("", snapW, snapH, thumb); err != nil {
			b.logf("camera: polaroid not shown: %v", err)
		}
	}
	return "This is the photo Famyy's camera just took of what is in front of it (it is not saved).", jpg, "image/jpeg", nil
}
