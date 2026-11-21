package consumer

import (
	"slices"
	"testing"

	"github.com/StevenBuglione/spring-framework-gpp/gpp/java"
	"github.com/StevenBuglione/spring-framework-gpp/org/springframework/jdbc/core/metadata"
)

func TestSpringTableParameterMetaData(t *testing.T) {
	name := java.StringFromUTF16([]uint16{0x0069, 0x0000, 0xd800})
	parameter := metadata.NewTableParameterMetaDataWithStringAndIntAndBoolean(name, 12, true)
	if parameter.GetParameterName() != name || parameter.GetSqlType() != 12 || !parameter.IsNullable() {
		t.Fatal("parameter metadata lost its name identity, SQL type, or nullability")
	}
	if got := parameter.GetParameterName().UTF16(); !slices.Equal(got, []uint16{0x0069, 0x0000, 0xd800}) {
		t.Fatalf("parameter name code units = %04x", got)
	}
	var nullName *java.String
	unnamed := metadata.NewTableParameterMetaDataWithStringAndIntAndBoolean(nullName, -5, false)
	if unnamed.GetParameterName() != nil || unnamed.GetSqlType() != -5 || unnamed.IsNullable() {
		t.Fatal("parameter metadata did not preserve a null Java name and a false flag")
	}
}
