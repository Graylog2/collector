// Copyright (C)  2026 Graylog, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the Server Side Public License, version 1,
// as published by MongoDB, Inc.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// Server Side Public License for more details.
//
// You should have received a copy of the Server Side Public License
// along with this program. If not, see
// <http://www.mongodb.com/licensing/server-side-public-license>.
//
// SPDX-License-Identifier: SSPL-1.0

package supervisor

import (
	"cmp"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/Graylog2/collector/superv/sysinfo"
	"github.com/Graylog2/collector/superv/version"
	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/shirou/gopsutil/v4/host"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.uber.org/zap"
)

// createAgentDescription creates the initial agent description for OpAMP.
func (s *Supervisor) createAgentDescription() *protobufs.AgentDescription {
	hostname, _ := os.Hostname()

	return &protobufs.AgentDescription{
		IdentifyingAttributes: []*protobufs.KeyValue{
			attributeStringKv(semconv.ServiceNameKey, ServiceName),
			attributeStringKv(semconv.ServiceInstanceIDKey, s.instanceUID),
		},
		NonIdentifyingAttributes: s.nonIdentifyingAttributes(hostname),
	}
}

// nonIdentifyingAttributes builds the list of non-identifying attributes for the agent description.
func (s *Supervisor) nonIdentifyingAttributes(hostname string) []*protobufs.KeyValue {
	attrs := []*protobufs.KeyValue{
		attributeStringKv(semconv.HostArchKey, runtime.GOARCH),
		attributeStringKv(semconv.HostNameKey, hostname),
		attributeStringKv(semconv.OSTypeKey, runtime.GOOS),
		attributeStringKv(semconv.ServiceVersionKey, version.Version()),
	}

	if description, err := getOSDescription(runtime.GOOS, host.Info, sysinfo.GetOSRelease); err == nil {
		attrs = append(attrs, attributeStringKv(semconv.OSDescriptionKey, description))
	} else {
		s.logger.Warn("Failed to determine the OS description", zap.Error(err))
	}

	if s.collectorVersion != "" {
		attrs = append(attrs, stringKv("collector.version", s.collectorVersion))
	}

	return attrs
}

// getOSDescription builds an "os.description" value for each platform.
func getOSDescription(goos string, infoSupplier func() (*host.InfoStat, error), osReleaseSupplier func() (sysinfo.OSRelease, error)) (string, error) {
	// On Linux we prefer data from the /etc/os-release file to get properly
	// formatted distribution names. All os-release fields are optional per the
	// spec, so we fall through to the generic host info data if the file can't
	// be read or yields nothing.
	if goos == "linux" {
		if osRelease, err := osReleaseSupplier(); err == nil {
			description := cmp.Or(strings.TrimSpace(osRelease.Name+" "+osRelease.VersionID), osRelease.PrettyName)
			if description != "" {
				return description, nil
			}
		}
	}

	info, err := infoSupplier()
	if err != nil {
		return "", fmt.Errorf("couldn't read host info: %w", err)
	}
	switch goos {
	case "darwin":
		return strings.TrimSpace("macOS " + info.PlatformVersion), nil
	case "windows":
		return strings.TrimSpace(info.Platform + " " + info.PlatformVersion), nil
	default:
		return cmp.Or(strings.TrimSpace(info.Platform+" "+info.PlatformVersion), "Unknown "+info.OS), nil
	}
}

// stringKv returns a protobufs.KeyValue for the given key and value.
func stringKv(key, value string) *protobufs.KeyValue {
	return &protobufs.KeyValue{
		Key:   key,
		Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: value}},
	}
}

// attributeStringKv returns a protobufs.KeyValue for the given attribute.Key and string value.
func attributeStringKv(key attribute.Key, value string) *protobufs.KeyValue {
	return stringKv(string(key), value)
}
