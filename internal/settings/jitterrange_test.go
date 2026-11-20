package settings

import (
	"encoding/xml"
	"os"
	"strconv"
	"testing"
)

// The jitter limits the preferences offer are the schema's range.
func TestJitterTargetFramesRange(t *testing.T) {
	raw, err := os.ReadFile(schemaFilePath(t))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Keys []struct {
			Name  string `xml:"name,attr"`
			Range struct {
				Min string `xml:"min,attr"`
				Max string `xml:"max,attr"`
			} `xml:"range"`
		} `xml:"schema>key"`
	}
	if err := xml.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for _, k := range file.Keys {
		if k.Name != "jitter-target-frames" {
			continue
		}
		if k.Range.Min != strconv.Itoa(JitterTargetFramesMin) || k.Range.Max != strconv.Itoa(JitterTargetFramesMax) {
			t.Errorf("schema range %s–%s, constants %d–%d",
				k.Range.Min, k.Range.Max, JitterTargetFramesMin, JitterTargetFramesMax)
		}
		return
	}
	t.Fatal("no jitter-target-frames key in the schema")
}
