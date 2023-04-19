// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package network

import (
	"fmt"
	"github.com/rs/zerolog/log"
	"io"
	"strings"
	"time"
)

type DelayOpts struct {
	Filter
	Delay      time.Duration
	Jitter     time.Duration
	Interfaces []string
}

const handleExclude = "1:1"
const handleInclude = "1:3"

func (o *DelayOpts) IpCommands(_ Family, _ Mode) (io.Reader, error) {
	return nil, nil
}

func (o *DelayOpts) TcCommands(mode Mode) (io.Reader, error) {
	var cmds []string

	for _, ifc := range o.Interfaces {
		cmds = append(cmds, fmt.Sprintf("qdisc %s dev %s root handle 1: prio", mode, ifc))
		cmds = append(cmds, fmt.Sprintf("qdisc %s dev %s parent %s handle 30: netem delay %dms %dms", mode, ifc, handleInclude, o.Delay.Milliseconds(), o.Jitter.Milliseconds()))

		if filterCmds, err := tcFilterCommands(o.Exclude, mode, ifc, "1:", handleExclude, len(cmds)); err == nil {
			cmds = append(cmds, filterCmds...)
		} else {
			return nil, err
		}

		if filterCmds, err := tcFilterCommands(o.Include, mode, ifc, "1:", handleInclude, len(cmds)); err == nil {
			cmds = append(cmds, filterCmds...)
		} else {
			return nil, err
		}
	}

	log.Debug().Strs("commands", cmds).Msg("generated tc commands")
	return toReader(cmds, mode)
}

func (o *DelayOpts) String() string {
	var sb strings.Builder
	sb.WriteString("Delaying traffic (delay: ")
	sb.WriteString(o.Delay.String())
	sb.WriteString(", Jitter: ")
	sb.WriteString(o.Jitter.String())
	sb.WriteString(")")
	sb.WriteString("\nto/from:\n")
	for _, inc := range o.Include {
		sb.WriteString(" ")
		sb.WriteString(inc.String())
		sb.WriteString("\n")
	}
	if len(o.Exclude) > 0 {
		sb.WriteString("but not from/to:\n")
		for _, exc := range o.Exclude {
			sb.WriteString(" ")
			sb.WriteString(exc.String())
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
