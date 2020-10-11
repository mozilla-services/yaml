package yaml_test

import (
	"github.com/mozilla-services/yaml"
	. "gopkg.in/check.v1"
)


var COMMENT_1_IN = []byte(`---
# begin
a:
 # foo
 # bar
 b:
 # baz
 c:
   foo: bar
   # asdf
 # bang
d:
 # a
 # b
 - 1
 # c
 - - 123
   # f
 # d
 - 2
 # e
`)
var COMMENT_1_TREE = []yaml.MapSlice{
	yaml.MapSlice{
		yaml.MapItem{
			Key:   yaml.Comment{
				Value: " begin",
			},
			Value: nil,
		},
		yaml.MapItem{
			Key:   "a",
			Value: yaml.MapSlice{
				yaml.MapItem{
					Key:   yaml.Comment{
						Value: " foo",
					},
					Value: nil,
				},
				yaml.MapItem{
					Key:   yaml.Comment{
						Value: " bar",
					},
					Value: nil,
				},
				yaml.MapItem{
					Key:   "b",
					Value: nil,
				},
				yaml.MapItem{
					Key:   yaml.Comment{
						Value: " baz",
					},
					Value: nil,
				},
				yaml.MapItem{
					Key:   "c",
					Value: yaml.MapSlice{
						yaml.MapItem{
							Key:   "foo",
							Value: "bar",
						},
						yaml.MapItem{
							Key:   yaml.Comment{
								Value: " asdf",
							},
							Value: nil,
						},
						yaml.MapItem{
							Key:   yaml.Comment{
								Value: " bang",
							},
							Value: nil,
						},
					},
				},
			},
		},
		yaml.MapItem{
			Key:   "d",
			Value: []interface{}{
				yaml.Comment{
					Value: " a",
				},
				yaml.Comment{
					Value: " b",
				},
				1,
				yaml.Comment{
					Value: " c",
				},
				[]interface{}{
					123,
					yaml.Comment{
						Value: " f",
					},
					yaml.Comment{
						Value: " d",
					},
				},
				2,
				yaml.Comment{
					Value: " e",
				},
			},
		},
	},
}
var COMMENT_1_OUT = []byte(`# begin
a:
  # foo
  # bar
  b: null
  # baz
  c:
    foo: bar
    # asdf
    # bang
d:
# a
# b
- 1
# c
- - 123
  # f
  # d
- 2
# e
`)

var COMMENT_2_IN = []byte(`# beginning
a:
    ## foo
    ##
    b:
`)
var COMMENT_2_TREE = []yaml.MapSlice{
	yaml.MapSlice{
		yaml.MapItem{
			Key:   yaml.Comment{
				Value: " beginning",
			},
			Value: nil,
		},
		yaml.MapItem{
			Key:   "a",
			Value: yaml.MapSlice{
				yaml.MapItem{
					Key:   yaml.Comment{
						Value: "# foo",
					},
					Value: nil,
				},
				yaml.MapItem{
					Key:   yaml.Comment{
						Value: "#",
					},
					Value: nil,
				},
				yaml.MapItem{
					Key:   "b",
					Value: nil,
				},
			},
		},
	},
}
var COMMENT_2_OUT = []byte(`# beginning
a:
  ## foo
  ##
  b: null
`)

var COMMENT_3_IN = []byte(`hello: world
---
hello: world
# ✅
---
hello: world
`)
var COMMENT_3_TREE = []yaml.MapSlice{
	yaml.MapSlice{
		yaml.MapItem{
			Key:   "hello",
			Value: "world",
		},
	},
	yaml.MapSlice{
		yaml.MapItem{
			Key:   "hello",
			Value: "world",
		},
		yaml.MapItem{
			Key:   yaml.Comment{
				Value: " ✅",
			},
			Value: nil,
		},
	},
	yaml.MapSlice{
		yaml.MapItem{
			Key:   "hello",
			Value: "world",
		},
	},
}
var COMMENT_3_OUT = []byte(`hello: world
# ✅
`)

func (s *S) TestCommentMoving1(c *C) {
	var data []yaml.MapSlice
	err := (yaml.CommentUnmarshaler{}).UnmarshalDocuments(COMMENT_1_IN, &data)
	c.Assert(err, DeepEquals, nil)
	c.Assert(data, DeepEquals, COMMENT_1_TREE)
	out, err := (&yaml.YAMLMarshaler{Indent: 2}).Marshal(data[0])
	c.Assert(err, DeepEquals, nil)
	c.Assert(out, DeepEquals, COMMENT_1_OUT)
}


func (s *S) TestCommentParsing(c *C) {
	var data []yaml.MapSlice
	err := (yaml.CommentUnmarshaler{}).UnmarshalDocuments(COMMENT_2_IN, &data)
	c.Assert(err, DeepEquals, nil)
	c.Assert(data, DeepEquals, COMMENT_2_TREE)
	out, err := (&yaml.YAMLMarshaler{Indent: 2}).Marshal(data[0])
	c.Assert(err, DeepEquals, nil)
	c.Assert(out, DeepEquals, COMMENT_2_OUT)
}

func (s *S) TestCommentUnicode(c *C) {
	var data []yaml.MapSlice
	err := (yaml.CommentUnmarshaler{}).UnmarshalDocuments(COMMENT_3_IN, &data)
	c.Assert(err, DeepEquals, nil)
	c.Assert(data, DeepEquals, COMMENT_3_TREE)
	out, err := (&yaml.YAMLMarshaler{Indent: 2}).Marshal(data[1])
	c.Assert(err, DeepEquals, nil)
	c.Assert(out, DeepEquals, COMMENT_3_OUT)
}
