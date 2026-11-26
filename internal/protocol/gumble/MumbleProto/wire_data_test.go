package MumbleProto_test

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/one337/murmel/internal/protocol/gumble/MumbleProto"
)

// wireCases holds every message of Mumble.proto with each field set, and
// the bytes it has on the wire.
var wireCases = []struct {
	msg  any
	wire string
}{
	{new(MumbleProto.Version),
		"0880808001120966656c64203420c3a41a0966656c64203620c3a4220966656c" +
			"64203820c3a4"},
	{new(MumbleProto.UDPTunnel),
		"0a030102ff"},
	{new(MumbleProto.Authenticate),
		"0a0966656c64203220c3a4120966656c64203420c3a41a0966656c64203620c3" +
			"a41a0966656c64203720c3a42009200a2801"},
	{new(MumbleProto.Ping),
		"0880808001108080800218808080032080808004288080800530808080063880" +
			"80800745000082414d00009241550000a2415d0000b241"},
	{new(MumbleProto.Reject),
		"0802120966656c64203420c3a4"},
	{new(MumbleProto.ServerSync),
		"088080800110808080021a0966656c64203620c3a42080808004"},
	{new(MumbleProto.ChannelRemove),
		"0880808001"},
	{new(MumbleProto.ChannelState),
		"088080800110808080021a0966656c64203620c3a42080808004208080c0042a" +
			"0a66656c6420313120c3a4308080c00630808080073880808008388080c00840" +
			"01481552031617ff588080800c60016801"},
	{new(MumbleProto.UserRemove),
		"088080800110808080021a0966656c64203620c3a42001"},
	{new(MumbleProto.UserState),
		"088080800110808080021a0966656c64203620c3a42080808004288080800530" +
			"0138014001480150015a031516ff62031617ff6a0a66656c6420323420c3a472" +
			"0a66656c6420323620c3a47a0a66656c6420323820c3a48201031d1eff8a0103" +
			"1e1fff900101980101a2010a66656c6420333620c3a4a2010a66656c64203337" +
			"20c3a4"},
	{new(MumbleProto.BanList),
		"0a3d0a030304ff108080c0021a0966656c64203720c3a4220966656c64203920" +
			"c3a42a0a66656c6420313120c3a4320a66656c6420313320c3a4388080c0070a" +
			"3f0a031112ff108080c0091a0a66656c6420323120c3a4220a66656c64203233" +
			"20c3a42a0a66656c6420323520c3a4320a66656c6420323720c3a4388080c00e" +
			"1001"},
	{new(MumbleProto.BanList_BanEntry),
		"0a030102ff108080c0011a0966656c64203520c3a4220966656c64203720c3a4" +
			"2a0966656c64203920c3a4320a66656c6420313120c3a4388080c006"},
	{new(MumbleProto.TextMessage),
		"08808080011080808002108080c002188080c003188080800420808080052080" +
			"80c0052a0a66656c6420313320c3a4"},
	{new(MumbleProto.PermissionDenied),
		"088080800110808080021880808003220966656c64203820c3a4280a320a6665" +
			"6c6420313220c3a4"},
	{new(MumbleProto.ACL),
		"088080800110011a2f0a0966656c64203820c3a4100118012001288080800828" +
			"8080c008308080c009308080800a388080800b388080c00b1a300a0a66656c64" +
			"20323620c3a41001180120012880808011288080c011308080c0123080808013" +
			"3880808014388080c0142221080110011801208080c0192a0a66656c64203533" +
			"20c3a4308080c01b388080c01c222108011001180120808080212a0a66656c64" +
			"20363820c3a4308080802338808080242801"},
	{new(MumbleProto.ACL_ChanGroup),
		"0a0966656c64203220c3a41001180120012880808005288080c005308080c006" +
			"30808080073880808008388080c008"},
	{new(MumbleProto.ACL_ChanACL),
		"08011001180120808080042a0a66656c6420313020c3a4308080800638808080" +
			"07"},
	{new(MumbleProto.QueryUsers),
		"0880808001088080c001120966656c64203520c3a4120966656c64203620c3a4"},
	{new(MumbleProto.CryptSetup),
		"0a030102ff12030203ff1a030304ff"},
	{new(MumbleProto.ContextActionModify),
		"0a0966656c64203220c3a4120966656c64203420c3a418808080032008"},
	{new(MumbleProto.ContextAction),
		"088080800110808080021a0966656c64203620c3a4"},
	{new(MumbleProto.UserList),
		"0a200880808002120966656c64203620c3a41a0966656c64203820c3a4208080" +
			"80050a22088080c006120a66656c6420313520c3a41a0a66656c6420313720c3" +
			"a4208080c009"},
	{new(MumbleProto.UserList_User),
		"0880808001120966656c64203420c3a41a0966656c64203620c3a42080808004"},
	{new(MumbleProto.VoiceTarget),
		"0880808001121f0880808003088080c003108080c0041a0a66656c6420313120" +
			"c3a420012801121f0880808009088080c009108080c00a1a0a66656c64203233" +
			"20c3a420012801"},
	{new(MumbleProto.VoiceTarget_Target),
		"0880808001088080c001108080c0021a0966656c64203720c3a420012801"},
	{new(MumbleProto.PermissionQuery),
		"088080800110808080021801"},
	{new(MumbleProto.CodecVersion),
		"0802100418012001"},
	{new(MumbleProto.UserStats),
		"088080800110011a030607ff1a030708ff221408808080051080808006188080" +
			"800720808080082a14088080c009108080c00a188080c00b208080c00c308080" +
			"c00d388080c00e450000fa414d000005425500000d425d000015426229088080" +
			"8014120a66656c6420343220c3a41a0a66656c6420343420c3a4220a66656c64" +
			"20343620c3a46830683172033233ff788080801a80018080801b88018080801c" +
			"900101980101"},
	{new(MumbleProto.UserStats_Stats),
		"0880808001108080800218808080032080808004"},
	{new(MumbleProto.RequestBlob),
		"0880808001088080c001108080c00210808080031880808004188080c004"},
	{new(MumbleProto.ServerConfig),
		"0880808001120966656c64203420c3a418012080808004288080800530808080" +
			"06"},
	{new(MumbleProto.SuggestConfig),
		"088080800110011801"},
}

// unknownField is field 99 with the varint 42, a field no message has.
const unknownField = "\x98\x06\x2a"

// fill sets every field of the message m points to, with values that
// differ from field to field; repeated fields get two entries.
func fill(m any) {
	n := 0
	fillStruct(reflect.ValueOf(m).Elem(), &n, 0)
}

func fillStruct(v reflect.Value, n *int, depth int) {
	for i := 0; i < v.NumField(); i++ {
		if f := v.Type().Field(i); f.IsExported() && !strings.HasPrefix(f.Name, "XXX_") {
			fillValue(v.Field(i), n, depth)
		}
	}
}

func fillValue(v reflect.Value, n *int, depth int) {
	*n++
	switch v.Kind() {
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		if p.Elem().Kind() == reflect.Struct {
			fillStruct(p.Elem(), n, depth+1)
		} else {
			fillValue(p.Elem(), n, depth)
		}
		v.Set(p)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			v.SetBytes([]byte{byte(*n), byte(*n + 1), 0xff})
			return
		}
		s := reflect.MakeSlice(v.Type(), 2, 2)
		for i := 0; i < 2; i++ {
			fillValue(s.Index(i), n, depth)
		}
		v.Set(s)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int32, reflect.Int64:
		v.SetInt(int64(*n))
	case reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(*n) << 20)
	case reflect.Float32:
		v.SetFloat(float64(*n) + 0.25)
	case reflect.String:
		v.SetString(fmt.Sprintf("feld %d ä", *n))
	default:
		panic("fill: unexpected kind " + v.Kind().String())
	}
}

// sameFields reports whether a and b, pointers to messages of one type,
// hold the same field values; bookkeeping fields are not compared.
func sameFields(a, b any) bool {
	return sameValue(reflect.ValueOf(a), reflect.ValueOf(b))
}

func sameValue(a, b reflect.Value) bool {
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return sameValue(a.Elem(), b.Elem())
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if f := a.Type().Field(i); f.IsExported() && !strings.HasPrefix(f.Name, "XXX_") &&
				!sameValue(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !sameValue(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	default:
		return a.Interface() == b.Interface()
	}
}
