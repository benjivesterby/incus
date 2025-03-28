package ip

import (
	"strings"

	"github.com/lxc/incus/v6/shared/subprocess"
)

// Route represents arguments for route manipulation.
type Route struct {
	DevName string
	Route   string
	Table   string
	Src     string
	Proto   string
	Family  string
	Via     string
	VRF     string
}

// Add adds new route.
func (r *Route) Add() error {
	cmd := []string{r.Family, "route", "add"}
	if r.Table != "" {
		cmd = append(cmd, "table", r.Table)
	}

	if r.Via != "" {
		cmd = append(cmd, "via", r.Via)
	}

	cmd = append(cmd, r.Route, "dev", r.DevName)
	if r.Src != "" {
		cmd = append(cmd, "src", r.Src)
	}

	if r.Proto != "" {
		cmd = append(cmd, "proto", r.Proto)
	}

	if r.VRF != "" {
		cmd = append(cmd, "vrf", r.VRF)
	}

	_, err := subprocess.RunCommand("ip", cmd...)
	if err != nil {
		return err
	}

	return nil
}

// Delete deletes routing table.
func (r *Route) Delete() error {
	cmd := []string{r.Family, "route", "delete", r.Route, "dev", r.DevName}

	if r.VRF != "" {
		cmd = append(cmd, "vrf", r.VRF)
	} else if r.Table != "" {
		cmd = append(cmd, "table", r.Table)
	}

	_, err := subprocess.RunCommand("ip", cmd...)
	if err != nil {
		return err
	}

	return nil
}

// Flush flushes routing tables.
func (r *Route) Flush() error {
	cmd := []string{}
	if r.Family != "" {
		cmd = append(cmd, r.Family)
	}

	cmd = append(cmd, "route", "flush")
	if r.Route != "" {
		cmd = append(cmd, r.Route)
	}

	if r.Via != "" {
		cmd = append(cmd, "via", r.Via)
	}

	cmd = append(cmd, "dev", r.DevName)
	if r.Proto != "" {
		cmd = append(cmd, "proto", r.Proto)
	}

	if r.VRF != "" {
		cmd = append(cmd, "vrf", r.VRF)
	}

	_, err := subprocess.RunCommand("ip", cmd...)
	if err != nil {
		return err
	}

	return nil
}

// Replace changes or adds new route.
func (r *Route) Replace(routes []string) error {
	cmd := []string{r.Family, "route", "replace", "dev", r.DevName, "proto", r.Proto}

	if r.VRF != "" {
		cmd = append(cmd, "vrf", r.VRF)
	}

	cmd = append(cmd, routes...)
	_, err := subprocess.RunCommand("ip", cmd...)
	if err != nil {
		return err
	}

	return nil
}

// Show lists routes.
func (r *Route) Show() ([]string, error) {
	routes := []string{}

	cmd := []string{r.Family, "route", "show", "dev", r.DevName, "proto", r.Proto}

	if r.VRF != "" {
		cmd = append(cmd, "vrf", r.VRF)
	}

	out, err := subprocess.RunCommand("ip", cmd...)
	if err != nil {
		return routes, err
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		route := strings.ReplaceAll(line, "linkdown", "")
		routes = append(routes, route)
	}

	return routes, nil
}
