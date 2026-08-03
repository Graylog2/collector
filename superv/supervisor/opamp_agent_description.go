package supervisor

import (
	"os"
	"runtime"
	"strings"

	"github.com/Graylog2/collector/superv/version"
	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/shirou/gopsutil/v4/host"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.uber.org/zap"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
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

	info, err := host.Info()
	if err == nil {
		attrs = append(attrs, attributeStringKv(semconv.OSDescriptionKey, getOSDescription(info)))
	} else {
		s.logger.Warn("Failed to retrieve host information", zap.Error(err))
	}

	if s.collectorVersion != "" {
		attrs = append(attrs, stringKv("collector.version", s.collectorVersion))
	}

	return attrs
}

// getOSDescription builds an "os.description" value for each platform.
func getOSDescription(info *host.InfoStat) string {
	switch info.OS {
	case "darwin":
		return strings.TrimSpace("macOS " + info.PlatformVersion)
	case "linux":
		return strings.TrimSpace(cases.Title(language.English).String(info.Platform) + " " + info.PlatformVersion)
	case "windows":
		return strings.TrimSpace(info.Platform + " " + info.PlatformVersion)
	default:
		return "Unknown " + info.OS
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
