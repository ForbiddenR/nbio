// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package nbio

import (
	"net"
	"runtime"
	"strings"

	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/timer"
)

// Start inits and starts pollers.
func (g *Engine) Start() error {
	// Create listener pollers.
	udpListeners := make([]*net.UDPConn, len(g.Addrs))[0:0]
	switch g.Network {
	case "tcp", "tcp4", "tcp6":
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

	// g.Timer.Start()

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
	cpuNum := runtime.NumCPU()
	if conf.Name == "" {
		conf.Name = "NB"
	}
	if conf.NPoller <= 0 {
		conf.NPoller = cpuNum
	}
	if conf.ReadBufferSize <= 0 {
		conf.ReadBufferSize = DefaultReadBufferSize
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
		Config:    conf,
		Timer:     timer.New(conf.Name),
		listeners: make([]*poller, len(conf.Addrs))[0:0],
		pollers:   make([]*poller, conf.NPoller),
		connsStd:  map[*Conn]struct{}{},
	}

	g.initHandlers()

	g.OnReadBufferAlloc(func(c *Conn) []byte {
		if c.ReadBuffer == nil {
			c.ReadBuffer = make([]byte, g.ReadBufferSize)
		}
		return c.ReadBuffer
	})

	return g
}
