// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"net"
	"runtime"
	"strings"

	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/taskpool"
	"github.com/lesismal/nbio/timer"
)

// Start inits and starts pollers.
func (g *Engine) Start() error {
	g.connsUnix = make([]*Conn, MaxOpenFiles)

	// Create pollers and listeners.
	g.pollers = make([]*poller, g.NPoller)
	g.listeners = make([]*poller, len(g.Addrs))[0:0]
	udpListeners := make([]*net.UDPConn, len(g.Addrs))[0:0]

	switch g.Network {
	case "unix", "tcp", "tcp4", "tcp6":
		for i := range g.Addrs {
			ln, err := newPoller(g, true, i)
			if err != nil {
				for j := 0; j < i; j++ {
					g.listeners[j].stop()
				}
				return err
			}
			g.Addrs[i] = ln.listener.Addr().String()
			g.listeners = append(g.listeners, ln)
		}
	case "udp", "udp4", "udp6":
		for i, addrStr := range g.Addrs {
			addr, err := net.ResolveUDPAddr(g.Network, addrStr)
			if err != nil {
				for j := 0; j < i; j++ {
					udpListeners[j].Close()
				}
				return err
			}
			ln, err := g.ListenUDP("udp", addr)
			if err != nil {
				for j := 0; j < i; j++ {
					udpListeners[j].Close()
				}
				return err
			}
			g.Addrs[i] = ln.LocalAddr().String()
			udpListeners = append(udpListeners, ln)
		}
	}

	// Create IO pollers.
	for i := 0; i < g.NPoller; i++ {
		p, err := newPoller(g, false, i)
		if err != nil {
			for j := 0; j < len(g.listeners); j++ {
				g.listeners[j].stop()
			}

			for j := 0; j < i; j++ {
				g.pollers[j].stop()
			}
			return err
		}
		g.pollers[i] = p
	}

	// Start IO pollers.
	for i := 0; i < g.NPoller; i++ {
		g.pollers[i].ReadBuffer = make([]byte, g.ReadBufferSize)
		g.Add(1)
		go g.pollers[i].start()
	}

	// Start TCP/Unix listener pollers.
	for _, l := range g.listeners {
		g.Add(1)
		go l.start()
	}

	// Start UDP listener pollers.
	for _, ln := range udpListeners {
		_, err := g.AddConn(ln)
		if err != nil {
			for j := 0; j < len(g.listeners); j++ {
				g.listeners[j].stop()
			}

			for j := 0; j < len(g.pollers); j++ {
				g.pollers[j].stop()
			}

			for j := 0; j < len(udpListeners); j++ {
				udpListeners[j].Close()
			}

			return err
		}
	}

	g.Timer.Start()
	g.isOneshot = (g.EpollMod == EPOLLET && g.EPOLLONESHOT == EPOLLONESHOT)

	if g.AsyncReadInPoller {
		if g.IOExecute == nil {
			g.ioTaskPool = taskpool.NewIO(0, 0, 0)
			g.IOExecute = g.ioTaskPool.Go
		}
	}

	if len(g.Addrs) == 0 {
		logging.Info("NBIO Engine[%v] start with [%v eventloop, MaxOpenFiles: %v]",
			g.Name,
			g.NPoller,
			MaxOpenFiles,
		)
	} else {
		logging.Info("NBIO Engine[%v] start with [%v eventloop], listen on: [\"%v@%v\"], MaxOpenFiles: %v",
			g.Name,
			g.NPoller,
			g.Network,
			strings.Join(g.Addrs, `", "`),
			MaxOpenFiles,
		)
	}

	return nil
}

// NewEngine creates an Engine and init default configurations.
func NewEngine(conf Config) *Engine {
	if conf.Name == "" {
		conf.Name = "NB"
	}
	if conf.NPoller <= 0 {
		conf.NPoller = runtime.NumCPU() / 4
		if conf.AsyncReadInPoller && conf.EpollMod == EPOLLET {
			conf.NPoller = 1
		}
		if conf.NPoller == 0 {
			conf.NPoller = 1
		}
	}
	if conf.ReadBufferSize <= 0 {
		conf.ReadBufferSize = DefaultReadBufferSize
	}
	if conf.MaxConnReadTimesPerEventLoop <= 0 {
		conf.MaxConnReadTimesPerEventLoop = DefaultMaxConnReadTimesPerEventLoop
	}
	if conf.Listen == nil {
		conf.Listen = net.Listen
	}
	if conf.ListenUDP == nil {
		conf.ListenUDP = net.ListenUDP
	}
	if conf.BodyAllocator == nil {
		conf.BodyAllocator = mempool.DefaultMemPool
	}

	g := &Engine{
		Config: conf,
		Timer:  timer.New(conf.Name),
	}

	g.initHandlers()

	return g
}
