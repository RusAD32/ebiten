// Copyright 2021 The Ebiten Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package go2cpp

import (
	"io"
	"runtime"
	"sync"
	"syscall/js"

	"github.com/hajimehoshi/oto/v2"
)

type Context struct {
	v               js.Value
	sampleRate      int
	channelCount    int
	bitDepthInBytes int
}

func NewContext(sampleRate int, channelCount, bitDepthInBytes int) *Context {
	v := js.Global().Get("go2cpp").Call("createAudio", sampleRate, channelCount, bitDepthInBytes)
	return &Context{
		v:               v,
		sampleRate:      sampleRate,
		channelCount:    channelCount,
		bitDepthInBytes: bitDepthInBytes,
	}
}

func (c *Context) NewPlayer(r io.Reader) oto.Player {
	cond := sync.NewCond(&sync.Mutex{})
	onwritten := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		cond.Signal()
		return nil
	})
	p := &Player{
		context:   c,
		src:       r,
		volume:    1,
		cond:      cond,
		onWritten: onwritten,
	}
	runtime.SetFinalizer(p, (*Player).Close)
	return p
}

func (c *Context) Suspend() error {
	// Do nothing so far.
	return nil
}

func (c *Context) Resume() error {
	// Do nothing so far.
	return nil
}

func (c *Context) Err() error {
	return nil
}

func (c *Context) oneBufferSize() int {
	// TODO: This must be audio.oneBufferSize(p.context.sampleRate). Avoid the duplication.
	return c.sampleRate * c.channelCount * c.bitDepthInBytes / 4
}

func (c *Context) MaxBufferSize() int {
	// TODO: This must be audio.maxBufferSize(p.context.sampleRate). Avoid the duplication.
	return c.oneBufferSize() * 2
}

type playerState int

const (
	playerStatePaused playerState = iota
	playerStatePlaying
	playerStateClosed
)

type Player struct {
	context *Context
	src     io.Reader
	v       js.Value
	state   playerState
	volume  float64
	cond    *sync.Cond
	err     error
	buf     []byte

	onWritten js.Func
}

func (p *Player) Pause() {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()

	if p.state == playerStateClosed {
		return
	}
	if !p.v.Truthy() {
		return
	}

	p.v.Call("pause")
	p.state = playerStatePaused
	p.cond.Signal()
}

func (p *Player) Play() {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()

	if p.state == playerStateClosed {
		return
	}

	var runloop bool
	if !p.v.Truthy() {
		p.v = p.context.v.Call("createPlayer", p.onWritten)
		runloop = true
	}

	p.v.Set("volume", p.volume)
	p.v.Call("play")

	// Prepare the first data as soon as possible, or the audio can get stuck.
	// TODO: Get the appropriate buffer size from the C++ side.
	if p.buf == nil {
		n := p.context.oneBufferSize()
		if max := p.context.MaxBufferSize() - p.UnplayedBufferSize(); n > max {
			n = max
		}
		p.buf = make([]byte, n)
	}
	n, err := p.src.Read(p.buf)
	if err != nil && err != io.EOF {
		p.setErrorImpl(err)
		return
	}
	if n > 0 {
		dst := js.Global().Get("Uint8Array").New(n)
		p.writeImpl(dst, p.buf[:n])
	}

	if runloop {
		go p.loop()
	}
	p.state = playerStatePlaying
	p.cond.Signal()
}

func (p *Player) IsPlaying() bool {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()

	return p.state == playerStatePlaying
}

func (p *Player) Reset() {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()

	if p.state == playerStateClosed {
		return
	}
	p.state = playerStatePaused

	if !p.v.Truthy() {
		return
	}

	p.v.Call("close", true)
	p.v = js.Undefined()
	p.cond.Signal()
}

func (p *Player) Volume() float64 {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()

	if !p.v.Truthy() {
		return p.volume
	}
	return p.v.Get("volume").Float()
}

func (p *Player) SetVolume(volume float64) {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()

	p.volume = volume
	if !p.v.Truthy() {
		return
	}
	p.v.Set("volume", volume)
}

func (p *Player) UnplayedBufferSize() int {
	if !p.v.Truthy() {
		return 0
	}
	return p.v.Get("unplayedBufferSize").Int()
}

func (p *Player) Err() error {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()

	return p.err
}

func (p *Player) Close() error {
	runtime.SetFinalizer(p, nil)
	return p.close(true)
}

func (p *Player) close(remove bool) error {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()
	return p.closeImpl(remove)
}

func (p *Player) closeImpl(remove bool) error {
	if p.state == playerStateClosed {
		return p.err
	}

	if p.v.Truthy() {
		p.v.Call("close", false)
		p.v = js.Undefined()
	}
	if remove {
		p.state = playerStateClosed
		p.onWritten.Release()
	} else {
		p.state = playerStatePaused
	}
	p.cond.Signal()
	return p.err
}

func (p *Player) setError(err error) {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()
	p.setErrorImpl(err)
}

func (p *Player) setErrorImpl(err error) {
	if p.state != playerStateClosed && p.v.Truthy() {
		p.v.Call("close", true)
		p.v = js.Undefined()
	}
	p.err = err
	p.state = playerStateClosed
	p.cond.Signal()
}

func (p *Player) shouldWait() bool {
	if !p.v.Truthy() {
		return false
	}
	switch p.state {
	case playerStatePaused:
		return true
	case playerStatePlaying:
		return p.v.Get("unplayedBufferSize").Int() >= p.context.MaxBufferSize()
	}
	return false
}

func (p *Player) waitUntilUnpaused() bool {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()

	for p.shouldWait() {
		p.cond.Wait()
	}
	return p.v.Truthy() && p.state == playerStatePlaying
}

func (p *Player) writeImpl(dst js.Value, src []byte) {
	if p.state == playerStateClosed {
		return
	}
	if !p.v.Truthy() {
		return
	}

	js.CopyBytesToJS(dst, src)
	p.v.Call("write", dst, len(src))
}

func (p *Player) loop() {
	const readChunkSize = 4096

	buf := make([]byte, readChunkSize)
	dst := js.Global().Get("Uint8Array").New(readChunkSize)

	for {
		if !p.waitUntilUnpaused() {
			return
		}

		p.cond.L.Lock()
		n, err := p.src.Read(buf)
		if err != nil && err != io.EOF {
			p.setErrorImpl(err)
			p.cond.L.Unlock()
			return
		}
		if n > 0 {
			p.writeImpl(dst, buf[:n])
		}

		if err == io.EOF {
			p.closeImpl(false)
			p.cond.L.Unlock()
			return
		}
		p.cond.L.Unlock()
	}
}
