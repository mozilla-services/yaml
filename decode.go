package yaml

import (
	"encoding"
	"encoding/base64"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"
)

const (
	documentNode = 1 << iota
	mappingNode
	sequenceNode
	scalarNode
	aliasNode
	commentNode
)

type node struct {
	kind         int
	line, column int
	tag          string
	value        string
	implicit     bool
	children     []*node
	anchors      map[string]*node
}

// ----------------------------------------------------------------------------
// Parser, produces a node tree out of a libyaml event stream.

type parser struct {
	parser yaml_parser_t
	event  yaml_event_t
	doc    *node
}

func newParser(b []byte, parse_comments bool) *parser {
	p := parser{}
	if !yaml_parser_initialize(&p.parser) {
		panic("failed to initialize YAML emitter")
	}
	p.parser.parse_comments = parse_comments

	if len(b) == 0 {
		b = []byte{'\n'}
	}

	yaml_parser_set_input_string(&p.parser, b)

	p.skip()
	if p.event.typ != yaml_STREAM_START_EVENT {
		panic("expected stream start event, got " + strconv.Itoa(int(p.event.typ)))
	}
	p.skip()
	return &p
}

func (p *parser) destroy() {
	if p.event.typ != yaml_NO_EVENT {
		yaml_event_delete(&p.event)
	}
	yaml_parser_delete(&p.parser)
}

func (p *parser) skip() {
	if p.event.typ != yaml_NO_EVENT {
		if p.event.typ == yaml_STREAM_END_EVENT {
			failf("attempted to go past the end of stream; corrupted value?")
		}
		yaml_event_delete(&p.event)
	}
	if !yaml_parser_parse(&p.parser, &p.event) {
		p.fail()
	}
}

func (p *parser) fail() {
	var where string
	var line int
	if p.parser.problem_mark.line != 0 {
		line = p.parser.problem_mark.line
	} else if p.parser.context_mark.line != 0 {
		line = p.parser.context_mark.line
	}
	if line != 0 {
		where = "line " + strconv.Itoa(line) + ": "
	}
	var msg string
	if len(p.parser.problem) > 0 {
		msg = p.parser.problem
	} else {
		msg = "unknown problem parsing YAML content"
	}
	failf("%s%s", where, msg)
}

func (p *parser) anchor(n *node, anchor []byte) {
	if anchor != nil {
		p.doc.anchors[string(anchor)] = n
	}
}

func (p *parser) parse() *node {
	switch p.event.typ {
	case yaml_SCALAR_EVENT:
		return p.scalar()
	case yaml_ALIAS_EVENT:
		return p.alias()
	case yaml_MAPPING_START_EVENT:
		return p.mapping()
	case yaml_SEQUENCE_START_EVENT:
		return p.sequence()
	case yaml_DOCUMENT_START_EVENT:
		return p.document()
	case yaml_STREAM_END_EVENT:
		// Happens when attempting to decode an empty buffer.
		return nil
	case yaml_COMMENT_EVENT:
		return p.comment()
	default:
		panic("attempted to parse unknown event: " + strconv.Itoa(int(p.event.typ)))
	}
	panic("unreachable")
}

func (p *parser) node(kind int) *node {
	return &node{
		kind:   kind,
		line:   p.event.start_mark.line,
		column: p.event.start_mark.column,
	}
}

func (p *parser) document() *node {
	n := p.node(documentNode)
	n.anchors = make(map[string]*node)
	p.doc = n
	p.skip()
	next := p.parse()
	for next.kind == commentNode {
		n.children = append(n.children, next)
		next = p.parse()
	}
	n.children = append(n.children, next)
	if p.event.typ != yaml_DOCUMENT_END_EVENT {
		panic("expected end of document event but got " + strconv.Itoa(int(p.event.typ)))
	}
	p.skip()
	return n
}

func (p *parser) alias() *node {
	n := p.node(aliasNode)
	n.value = string(p.event.anchor)
	p.skip()
	return n
}

func (p *parser) scalar() *node {
	n := p.node(scalarNode)
	n.value = string(p.event.value)
	n.tag = string(p.event.tag)
	n.implicit = p.event.implicit
	p.anchor(n, p.event.anchor)
	p.skip()
	return n
}

func (p *parser) comment() *node {
	n := p.node(commentNode)
	n.value = string(p.event.value)
	p.skip()
	return n
}

func (p *parser) sequence() *node {
	n := p.node(sequenceNode)
	p.anchor(n, p.event.anchor)
	p.skip()
	for p.event.typ != yaml_SEQUENCE_END_EVENT {
		n.children = append(n.children, p.parse())
	}
	p.skip()
	return n
}

func (p *parser) mapping() *node {
	n := p.node(mappingNode)
	p.anchor(n, p.event.anchor)
	p.skip()
	for p.event.typ != yaml_MAPPING_END_EVENT {
		n.children = append(n.children, p.parse())
	}
	p.skip()
	return n
}

// ----------------------------------------------------------------------------
// Decoder, unmarshals a node into a provided value.

type decoder struct {
	doc     *node
	aliases map[string]bool
	mapType reflect.Type
	terrors []string
}

var (
	mapItemType    = reflect.TypeOf(MapItem{})
	durationType   = reflect.TypeOf(time.Duration(0))
	defaultMapType = reflect.TypeOf(map[interface{}]interface{}{})
	ifaceType      = defaultMapType.Elem()
)

func newDecoder() *decoder {
	d := &decoder{mapType: defaultMapType}
	d.aliases = make(map[string]bool)
	return d
}

func (d *decoder) terror(n *node, tag string, out reflect.Value) {
	if n.tag != "" {
		tag = n.tag
	}
	value := n.value
	if tag != yaml_SEQ_TAG && tag != yaml_MAP_TAG {
		if len(value) > 10 {
			value = " `" + value[:7] + "...`"
		} else {
			value = " `" + value + "`"
		}
	}
	d.terrors = append(d.terrors, fmt.Sprintf("line %d: cannot unmarshal %s%s into %s", n.line+1, shortTag(tag), value, out.Type()))
}

func (d *decoder) callUnmarshaler(n *node, u Unmarshaler) (good bool) {
	terrlen := len(d.terrors)
	err := u.UnmarshalYAML(func(v interface{}) (err error) {
		defer handleErr(&err)
		d.unmarshal(n, reflect.ValueOf(v), nil)
		if len(d.terrors) > terrlen {
			issues := d.terrors[terrlen:]
			d.terrors = d.terrors[:terrlen]
			return &TypeError{issues}
		}
		return nil
	})
	if e, ok := err.(*TypeError); ok {
		d.terrors = append(d.terrors, e.Errors...)
		return false
	}
	if err != nil {
		fail(err)
	}
	return true
}

// d.prepare initializes and dereferences pointers and calls UnmarshalYAML
// if a value is found to implement it.
// It returns the initialized and dereferenced out value, whether
// unmarshalling was already done by UnmarshalYAML, and if so whether
// its types unmarshalled appropriately.
//
// If n holds a null value, prepare returns before doing anything.
func (d *decoder) prepare(n *node, out reflect.Value) (newout reflect.Value, unmarshaled, good bool) {
	if n.tag == yaml_NULL_TAG || n.kind == scalarNode && n.tag == "" && (n.value == "null" || n.value == "" && n.implicit) {
		return out, false, false
	}
	again := true
	for again {
		again = false
		if out.Kind() == reflect.Ptr {
			if out.IsNil() {
				out.Set(reflect.New(out.Type().Elem()))
			}
			out = out.Elem()
			again = true
		}
		if out.CanAddr() {
			if u, ok := out.Addr().Interface().(Unmarshaler); ok {
				good = d.callUnmarshaler(n, u)
				return out, true, good
			}
		}
	}
	return out, false, false
}

// Here and in the following, prependComments is a slice of comments marshaled
// as MapItems which should be prepended to the next map or sequence.
//
// This is because an input YAML such as
//
//     a:
//        # comment
//        b: c
//
// will result in the comment event being emitted before the map start event,
// which would otherwise cause the comment to be moved before the `a:`, or if
// evaluated differently, after the end of the `a:` map. In any case, the comment
// would be placed very wrongly. Now, the comment will be put into prependComments
// after receiving the event for `a`, and before the map start event, and end up
// as the first element in the unmarshaled map.

func (d *decoder) unmarshal(n *node, out reflect.Value, prependComments []MapItem) (good_ bool, prependComments_ []MapItem) {
	switch n.kind {
	case documentNode:
		return d.document(n, out), prependComments
	case aliasNode:
		return d.alias(n, out), prependComments
	}
	out, unmarshaled, good := d.prepare(n, out)
	if unmarshaled {
		return good, prependComments
	}
	switch n.kind {
	case scalarNode:
		good = d.scalar(n, out)
	case commentNode:
		good = d.comment(n, out)
	case mappingNode:
		good, prependComments = d.mapping(n, out, prependComments)
	case sequenceNode:
		good, prependComments = d.sequence(n, out, prependComments)
	default:
		panic("internal error: unknown node kind: " + strconv.Itoa(n.kind))
	}
	return good, prependComments
}

func (d *decoder) document(n *node, out reflect.Value) (good bool) {
	var prependComments []MapItem
	var hasDocument bool
	for _, node := range n.children {
		if node.kind == commentNode {
			prependComments = append(prependComments, MapItem{
				Key: Comment{
					Value: node.value,
				},
				Value: nil,
			})
		} else if (!hasDocument) {
			d.doc = n
			d.unmarshal(node, out, prependComments)
			hasDocument = true
		} else {
			return false
		}
	}
	return hasDocument
}

func (d *decoder) alias(n *node, out reflect.Value) (good bool) {
	an, ok := d.doc.anchors[n.value]
	if !ok {
		failf("unknown anchor '%s' referenced", n.value)
	}
	if d.aliases[n.value] {
		failf("anchor '%s' value contains itself", n.value)
	}
	d.aliases[n.value] = true
	good, _ = d.unmarshal(an, out, nil)
	delete(d.aliases, n.value)
	return good
}

var zeroValue reflect.Value

func resetMap(out reflect.Value) {
	for _, k := range out.MapKeys() {
		out.SetMapIndex(k, zeroValue)
	}
}

type Comment struct {
	Value string
}

func (d *decoder) comment(n *node, out reflect.Value) (good bool) {
	switch out.Kind() {
	case reflect.Interface:
		out.Set(reflect.ValueOf(Comment{
			Value: n.value,
		}))
	}
	return true
}

func (d *decoder) scalar(n *node, out reflect.Value) (good bool) {
	var tag string
	var resolved interface{}
	if n.tag == "" && !n.implicit {
		tag = yaml_STR_TAG
		resolved = n.value
	} else {
		tag, resolved = resolve(n.tag, n.value)
		if tag == yaml_BINARY_TAG {
			data, err := base64.StdEncoding.DecodeString(resolved.(string))
			if err != nil {
				failf("!!binary value contains invalid base64 data")
			}
			resolved = string(data)
		}
	}
	if resolved == nil {
		if out.Kind() == reflect.Map && !out.CanAddr() {
			resetMap(out)
		} else {
			out.Set(reflect.Zero(out.Type()))
		}
		return true
	}
	if s, ok := resolved.(string); ok && out.CanAddr() {
		if u, ok := out.Addr().Interface().(encoding.TextUnmarshaler); ok {
			err := u.UnmarshalText([]byte(s))
			if err != nil {
				fail(err)
			}
			return true
		}
	}
	switch out.Kind() {
	case reflect.String:
		if tag == yaml_BINARY_TAG {
			out.SetString(resolved.(string))
			good = true
		} else if resolved != nil {
			out.SetString(n.value)
			good = true
		}
	case reflect.Interface:
		if resolved == nil {
			out.Set(reflect.Zero(out.Type()))
		} else {
			out.Set(reflect.ValueOf(resolved))
		}
		good = true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch resolved := resolved.(type) {
		case int:
			if !out.OverflowInt(int64(resolved)) {
				out.SetInt(int64(resolved))
				good = true
			}
		case int64:
			if !out.OverflowInt(resolved) {
				out.SetInt(resolved)
				good = true
			}
		case uint64:
			if resolved <= math.MaxInt64 && !out.OverflowInt(int64(resolved)) {
				out.SetInt(int64(resolved))
				good = true
			}
		case float64:
			if resolved <= math.MaxInt64 && !out.OverflowInt(int64(resolved)) {
				out.SetInt(int64(resolved))
				good = true
			}
		case string:
			if out.Type() == durationType {
				d, err := time.ParseDuration(resolved)
				if err == nil {
					out.SetInt(int64(d))
					good = true
				}
			}
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		switch resolved := resolved.(type) {
		case int:
			if resolved >= 0 && !out.OverflowUint(uint64(resolved)) {
				out.SetUint(uint64(resolved))
				good = true
			}
		case int64:
			if resolved >= 0 && !out.OverflowUint(uint64(resolved)) {
				out.SetUint(uint64(resolved))
				good = true
			}
		case uint64:
			if !out.OverflowUint(uint64(resolved)) {
				out.SetUint(uint64(resolved))
				good = true
			}
		case float64:
			if resolved <= math.MaxUint64 && !out.OverflowUint(uint64(resolved)) {
				out.SetUint(uint64(resolved))
				good = true
			}
		}
	case reflect.Bool:
		switch resolved := resolved.(type) {
		case bool:
			out.SetBool(resolved)
			good = true
		}
	case reflect.Float32, reflect.Float64:
		switch resolved := resolved.(type) {
		case int:
			out.SetFloat(float64(resolved))
			good = true
		case int64:
			out.SetFloat(float64(resolved))
			good = true
		case uint64:
			out.SetFloat(float64(resolved))
			good = true
		case float64:
			out.SetFloat(resolved)
			good = true
		}
	case reflect.Ptr:
		if out.Type().Elem() == reflect.TypeOf(resolved) {
			// TODO DOes this make sense? When is out a Ptr except when decoding a nil value?
			elem := reflect.New(out.Type().Elem())
			elem.Elem().Set(reflect.ValueOf(resolved))
			out.Set(elem)
			good = true
		}
	}
	if !good {
		d.terror(n, tag, out)
	}
	return good
}

func settableValueOf(i interface{}) reflect.Value {
	v := reflect.ValueOf(i)
	sv := reflect.New(v.Type()).Elem()
	sv.Set(v)
	return sv
}

func (d *decoder) sequence(n *node, out reflect.Value, prependComments []MapItem) (good bool, prependComments_ []MapItem) {
	sl := len(n.children)
	l := sl + len(prependComments)

	var iface reflect.Value
	switch out.Kind() {
	case reflect.Slice:
		out.Set(reflect.MakeSlice(out.Type(), l, l))
	case reflect.Interface:
		// No type hints. Will have to use a generic sequence.
		iface = out
		out = settableValueOf(make([]interface{}, l))
	default:
		d.terror(n, yaml_SEQ_TAG, out)
		return false, nil
	}
	et := out.Type().Elem()
	j := 0
	if len(prependComments) > 0 {
		for _, prependCommentsElt := range prependComments {
			e := reflect.New(et).Elem()
			e.Set(reflect.ValueOf(prependCommentsElt.Key))
			out.Index(j).Set(e)
			j++
		}
		prependComments = nil
	}

	for i := 0; i < sl; i++ {
		e := reflect.New(et).Elem()
		if ok, _ := d.unmarshal(n.children[i], e, nil); ok {
			out.Index(j).Set(e)
			j++
		}
	}
	out.Set(out.Slice(0, j))
	if iface.IsValid() {
		iface.Set(out)
	}
	return true, prependComments
}

func (d *decoder) mapping(n *node, out reflect.Value, prependComments []MapItem) (good bool, prependComments_ []MapItem) {
	switch out.Kind() {
	case reflect.Struct:
		return d.mappingStruct(n, out, prependComments)
	case reflect.Slice:
		return d.mappingSlice(n, out, prependComments)
	case reflect.Map:
		// okay
	case reflect.Interface:
		if d.mapType.Kind() == reflect.Map {
			iface := out
			out = reflect.MakeMap(d.mapType)
			iface.Set(out)
		} else {
			slicev := reflect.New(d.mapType).Elem()
			var ok bool
			if ok, prependComments = d.mappingSlice(n, slicev, prependComments); !ok {
				return false, prependComments
			}
			out.Set(slicev)
			return true, prependComments
		}
	default:
		d.terror(n, yaml_MAP_TAG, out)
		return false, nil
	}
	outt := out.Type()
	kt := outt.Key()
	et := outt.Elem()

	mapType := d.mapType
	if outt.Key() == ifaceType && outt.Elem() == ifaceType {
		d.mapType = outt
	}

	if out.IsNil() {
		out.Set(reflect.MakeMap(outt))
	}
	var key *node
	var value *node
	var keySet bool
	for _, child := range n.children {
		if child.kind == commentNode {
			out.SetMapIndex(
				reflect.ValueOf(Comment{
					Value: child.value,
				}),
				reflect.Zero(kt))
		} else {
			if !keySet {
				keySet = true
				key = child
			} else {
				keySet = false
				value = child
				if isMerge(key) {
					d.merge(value, out)
					continue
				}
				k := reflect.New(kt).Elem()
				if good, _ := d.unmarshal(key, k, nil); good {
					kkind := k.Kind()
					if kkind == reflect.Interface {
						kkind = k.Elem().Kind()
					}
					if kkind == reflect.Map || kkind == reflect.Slice {
						failf("invalid map key: %#v", k.Interface())
					}
					e := reflect.New(et).Elem()
					if good, _ := d.unmarshal(value, e, nil); good {
						out.SetMapIndex(k, e)
					}
				}
			}
		}
	}
	d.mapType = mapType
	return true, prependComments
}

func (d *decoder) mappingSlice(n *node, out reflect.Value, prependComments []MapItem) (good bool, prependComments_ []MapItem) {
	outt := out.Type()
	if outt.Elem() != mapItemType {
		d.terror(n, yaml_MAP_TAG, out)
		return false, nil
	}

	mapType := d.mapType
	d.mapType = outt

	var slice []MapItem
	if len(prependComments) > 0 {
		slice = append(slice, prependComments...)
		prependComments = nil
	}

	var key *node
	var value *node
	var keySet bool
	for _, child := range n.children {
		if child.kind == commentNode {
			cmt := MapItem{
				Key: Comment{
					Value: child.value,
				},
				Value: nil,
			}
			if keySet {
				prependComments = append(prependComments, cmt)
			} else {
				slice = append(slice, cmt)
			}
		} else {
			if !keySet {
				keySet = true
				key = child
			} else {
				keySet = false
				value = child
				if isMerge(key) {
					d.merge(value, out)
					continue
				}
				item := MapItem{}
				k := reflect.ValueOf(&item.Key).Elem()
				if good, _ := d.unmarshal(key, k, nil); good {
					v := reflect.ValueOf(&item.Value).Elem()
					if good, prependComments = d.unmarshal(value, v, prependComments); good {
						slice = append(slice, item)
						if len(prependComments) > 0 {
							slice = append(slice, prependComments...)
							prependComments = nil
						}
					}
				}
			}
		}
	}
	if len(prependComments) > 0 {
		slice = append(slice, prependComments...)
		prependComments = nil
	}
	out.Set(reflect.ValueOf(slice))
	d.mapType = mapType
	return true, prependComments
}

func (d *decoder) mappingStruct(n *node, out reflect.Value, prependComments []MapItem) (good bool, prependComments_ []MapItem) {
	sinfo, err := getStructInfo(out.Type())
	if err != nil {
		panic(err)
	}
	name := settableValueOf("")
	l := len(n.children)

	var inlineMap reflect.Value
	var elemType reflect.Type
	if sinfo.InlineMap != -1 {
		inlineMap = out.Field(sinfo.InlineMap)
		inlineMap.Set(reflect.New(inlineMap.Type()).Elem())
		elemType = inlineMap.Type().Elem()
	}

	for i := 0; i < l; i += 2 {
		ni := n.children[i]
		if isMerge(ni) {
			d.merge(n.children[i+1], out)
			continue
		}
		if good, _ := d.unmarshal(ni, name, nil); !good {
			continue
		}
		if info, ok := sinfo.FieldsMap[name.String()]; ok {
			var field reflect.Value
			if info.Inline == nil {
				field = out.Field(info.Num)
			} else {
				field = out.FieldByIndex(info.Inline)
			}
			d.unmarshal(n.children[i+1], field, nil)
		} else if sinfo.InlineMap != -1 {
			if inlineMap.IsNil() {
				inlineMap.Set(reflect.MakeMap(inlineMap.Type()))
			}
			value := reflect.New(elemType).Elem()
			d.unmarshal(n.children[i+1], value, nil)
			inlineMap.SetMapIndex(name, value)
		}
	}
	return true, prependComments
}

func failWantMap() {
	failf("map merge requires map or sequence of maps as the value")
}

func (d *decoder) merge(n *node, out reflect.Value) {
	switch n.kind {
	case mappingNode:
		d.unmarshal(n, out, nil)
	case aliasNode:
		an, ok := d.doc.anchors[n.value]
		if ok && an.kind != mappingNode {
			failWantMap()
		}
		d.unmarshal(n, out, nil)
	case sequenceNode:
		// Step backwards as earlier nodes take precedence.
		for i := len(n.children) - 1; i >= 0; i-- {
			ni := n.children[i]
			if ni.kind == aliasNode {
				an, ok := d.doc.anchors[ni.value]
				if ok && an.kind != mappingNode {
					failWantMap()
				}
			} else if ni.kind != mappingNode {
				failWantMap()
			}
			d.unmarshal(ni, out, nil)
		}
	default:
		failWantMap()
	}
}

func isMerge(n *node) bool {
	return n.kind == scalarNode && n.value == "<<" && (n.implicit == true || n.tag == yaml_MERGE_TAG)
}
