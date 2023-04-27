// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package networkutils

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Mode string
type Family string

const (
	ModeAdd    Mode   = "add"
	ModeDelete Mode   = "del"
	FamilyV4   Family = "inet"
	FamilyV6   Family = "inet6"
)

type Opts interface {
	IpCommands(family Family, mode Mode) (io.Reader, error)
	TcCommands(mode Mode) (io.Reader, error)
	String() string
}

type Filter struct {
	Include []CidrWithPortRange
	Exclude []CidrWithPortRange
}

var (
	PortRangeAny = PortRange{From: 1, To: 65534}
)

type PortRange struct {
	From uint16
	To   uint16
}

func (p *PortRange) String() string {
	if p.From == p.To {
		return strconv.Itoa(int(p.From))
	}
	return fmt.Sprintf("%d-%d", p.From, p.To)
}

func ParsePortRange(raw string) (PortRange, error) {
	parts := strings.Split(raw, "-")
	if len(parts) > 2 {
		return PortRange{}, errors.New("invalid port range")
	}

	from, err := strconv.Atoi(parts[0])
	if err != nil {
		return PortRange{}, err
	}

	to := from
	if len(parts) == 2 && parts[1] != "" {
		to, err = strconv.Atoi(parts[1])
		if err != nil {
			return PortRange{}, err
		}
	}

	if from < 1 || to > 65534 || from > to {
		return PortRange{}, errors.New("invalid port range")
	}

	return PortRange{From: uint16(from), To: uint16(to)}, nil
}

type CidrWithPortRange struct {
	Cidr      string
	PortRange PortRange
}

func (c CidrWithPortRange) String() string {
	if c.PortRange == PortRangeAny {
		return c.Cidr
	}
	return fmt.Sprintf("%s port %s", c.Cidr, c.PortRange.String())
}

func NewCidrWithPortRanges(cidrs []string, portRanges ...PortRange) []CidrWithPortRange {
	var result []CidrWithPortRange
	for _, cidr := range cidrs {
		for _, portRange := range portRanges {
			result = append(result, CidrWithPortRange{
				Cidr:      cidr,
				PortRange: portRange,
			})
		}
	}
	return result
}
