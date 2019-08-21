// Lute - A structured markdown engine.
// Copyright (C) 2019-present, b3log.org
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package lute

import (
	"html"
	"strings"
)

// parseInlines 解析并生成行级节点。
func (t *Tree) parseInlines() {
	t.context.delimiters = nil
	t.context.brackets = nil

	// 从根节点开始按深度优先遍历子块节点
	Walk(t.Root, func(n Node, entering bool) (WalkStatus, error) {
		if entering { // 遍历进入块节点时不做任何处理
			return WalkContinue, nil
		}

		// 遍历离开块节点时，只有如下几种类型的块节点需要生成行级子节点
		if typ := n.Type(); NodeParagraph == typ || NodeHeading == typ || NodeTableCell == typ {
			if NodeParagraph == typ && nil == n.Tokens() {
				// 解析 GFM 表节点后段落内容 tokens 可能会被置换为空（具体可参看 Paragraph.Finalize() 函数）
				// 在这里从语法树上移除空段落节点
				next := n.Next()
				n.Unlink()
				// Unlink 会将后一个兄弟节点置空，此处是在在遍历过程中修改树结构，所以需要保持继续迭代后面的兄弟节点
				n.SetNext(next)
				return WalkContinue, nil
			}

			// 逐个生成该块节点的行级子节点
			for t.parseInline(n) {
			}

			// 处理该块节点中的强调、加粗和删除线
			t.processEmphasis(nil)

			// 将连续的文本节点进行合并。
			// 规范只是定义了从输入的 Markdown 文本到输出的 HTML 的解析渲染规则，并未定义中间语法树的规则。
			// 也就是说语法树的节点结构没有标准，可以自己发挥。这里进行文本节点合并主要有两个目的：
			// 1. 减少节点数量，提升后续处理性能
			// 2. 方便后续功能方面的处理，比如 GFM 自动邮件链接解析
			for child := n.FirstChild(); nil != child; {
				next := child.Next()
				if NodeText == child.Type() {
					for nil != next && NodeText == next.Type() {
						child.AppendTokens(next.Tokens())
						next.Unlink()
						next = child.Next()
					}
				}
				child = next
			}

			if t.context.option.GFMAutoLink {
				// 处理 GFM 自动邮件链接
				for child := n.FirstChild(); nil != child; child = child.Next() {
					if NodeText == child.Type() {
						t.parseGfmAutoEmailLink(child)
					}
				}
			}
		}

		return WalkContinue, nil
	})
}

// parseInline 解析并生成块节点 block 的行级子节点。
func (t *Tree) parseInline(block Node) bool {
	tokens := block.Tokens()
	length := len(tokens)
	if 1 > length {
		return false
	}

	t.context.pos = 0
	for {
		token := tokens[t.context.pos]
		var n Node
		switch token {
		case itemBackslash:
			n = t.parseBackslash(tokens)
		case itemBacktick:
			n = t.parseCodeSpan(tokens)
		case itemAsterisk, itemUnderscore, itemTilde:
			t.handleDelim(block, tokens)
		case itemNewline:
			n = t.parseNewline(block, tokens)
		case itemLess:
			n = t.parseAutolink(tokens)
			if nil == n {
				n = t.parseAutoEmailLink(tokens)
				if nil == n {
					n = t.parseInlineHTML(tokens)
				}
			}
		case itemOpenBracket:
			n = t.parseOpenBracket(tokens)
		case itemCloseBracket:
			n = t.parseCloseBracket(tokens)
		case itemAmpersand:
			n = t.parseEntity(tokens)
		case itemBang:
			n = t.parseBang(tokens)
		default:
			if t.context.option.GFMAutoLink {
				n = t.parseGfmAutoLink(tokens, "www.")
				if nil == n {
					n = t.parseGfmAutoLink(tokens, "https://")
					if nil == n {
						n = t.parseGfmAutoLink(tokens, "http://")
						if nil == n {
							n = t.parseGfmAutoLink(tokens, "ftp://")
							if nil == n {
								n = t.parseText(tokens)
							}
						}
					}
				}
			} else {
				n = t.parseText(tokens)
			}
		}

		if nil != n {
			block.AppendChild(block, n)
		}

		if 1 > length || t.context.pos >= length || itemEnd == tokens[t.context.pos] {
			return false
		}
	}
}

func (t *Tree) parseEntity(tokens items) (ret Node) {
	length := len(tokens)
	if 2 > length || length <= t.context.pos+1 {
		t.context.pos++
		return &Text{tokens: toItems("&")}
	}

	start := t.context.pos
	numeric := false
	if 3 < length {
		numeric = itemCrosshatch == tokens[start+1]
	}
	i := t.context.pos
	var token byte
	var endWithSemicolon bool
	for ; i < length; i++ {
		token = tokens[i]
		if isWhitespace(token) {
			break
		}
		if itemSemicolon == token {
			i++
			endWithSemicolon = true
			break
		}
	}

	entityName := tokens[start:i].string()
	if entityValue, ok := htmlEntities[entityName]; ok { // 通过查表优化
		t.context.pos += i - start
		return &Text{tokens: toItems(entityValue)}
	}

	if !endWithSemicolon {
		t.context.pos++
		return &Text{tokens: toItems("&")}
	}

	if numeric {
		entityNameLen := len(entityName)
		if 10 < entityNameLen || 4 > entityNameLen {
			t.context.pos++
			return &Text{tokens: toItems("&")}
		}

		hex := 'x' == entityName[2] || 'X' == entityName[2]
		if hex {
			if 5 > entityNameLen {
				t.context.pos++
				return &Text{tokens: toItems("&")}
			}
		}
	}

	v := html.UnescapeString(entityName)
	if v == entityName {
		t.context.pos++
		return &Text{tokens: toItems("&")}
	}
	t.context.pos += i - start
	return &Text{tokens: toItems(v)}
}

// Try to match close bracket against an opening in the delimiter stack. Add either a link or image, or a plain [ character,
// to block's children. If there is a matching delimiter, remove it from the delimiter stack.
func (t *Tree) parseCloseBracket(tokens items) Node {
	// get last [ or ![
	opener := t.context.brackets
	if nil == opener {
		t.context.pos++
		// no matched opener, just return a literal
		return &Text{tokens: toItems("]")}
	}

	if !opener.active {
		t.context.pos++
		// no matched opener, just return a literal
		// take opener off brackets stack
		t.removeBracket()
		return &Text{tokens: toItems("]")}
	}

	// If we got here, open is a potential opener
	isImage := opener.image

	var dest, title string
	// Check to see if we have a link/image

	startPos := t.context.pos
	savepos := t.context.pos
	matched := false
	// Inline link?
	if t.context.pos+1 < len(tokens) && itemOpenParen == tokens[t.context.pos+1] {
		t.context.pos++
		isLink := false
		var passed, remains items
		if isLink, passed, remains = tokens[t.context.pos:].spnl(); isLink {
			t.context.pos += len(passed)
			if passed, remains, dest = t.context.parseInlineLink(remains); nil != passed {
				t.context.pos += len(passed)
				if 0 < len(remains) && isWhitespace(remains[0]) { // 跟空格的话后续尝试按 title 解析
					t.context.pos++
					if isLink, passed, remains = remains.spnl(); isLink {
						t.context.pos += len(passed)
						validTitle := false
						if validTitle, passed, remains, title = t.context.parseLinkTitle(remains); validTitle {
							t.context.pos += len(passed)
							isLink, passed, remains = remains.spnl()
							t.context.pos += len(passed)
							matched = isLink && itemCloseParen == remains[0]
						}
					}
				} else { // 没有 title
					t.context.pos--
					matched = true
				}
			}
		}
		if !matched {
			t.context.pos = savepos
		}
	}

	var reflabel string
	if !matched {
		// 尝试解析链接 label
		var beforelabel = t.context.pos + 1
		passed, _, label := t.context.parseLinkLabel(tokens[beforelabel:])
		var n = len(passed)
		if n > 0 { // label 解析出来的话说明满足格式 [text][label]
			reflabel = label
			t.context.pos += n + 1
		} else if !opener.bracketAfter {
			// [text][] 或者 [text][] 格式，将第一个 text 视为 label 进行解析
			passed = tokens[ opener.index:startPos]
			reflabel = fromItems(passed)
			if len(passed) > 0 && len(tokens) > beforelabel && itemOpenBracket == tokens[beforelabel] {
				// [text][] 格式，跳过 []
				t.context.pos += 2
			}
		}

		if "" != reflabel {
			// 查找链接引用
			var link = t.context.linkRefDef[strings.ToLower(reflabel)]
			if nil != link {
				dest = link.Destination
				title = link.Title
				matched = true
			}
		}
	}

	if matched {
		var node Node
		if isImage {
			node = &Image{&BaseNode{typ: NodeImage}, dest, title}
		} else {
			node = &Link{&BaseNode{typ: NodeLink}, dest, title}
		}

		var tmp, next Node
		tmp = opener.node.Next()
		for nil != tmp {
			next = tmp.Next()
			tmp.Unlink()
			node.AppendChild(node, tmp)
			tmp = next
		}

		t.processEmphasis(opener.previousDelimiter)
		t.removeBracket()
		opener.node.Unlink()

		// We remove this bracket and processEmphasis will remove later delimiters.
		// Now, for a link, we also deactivate earlier link openers.
		// (no links in links)
		if !isImage {
			opener = t.context.brackets
			for nil != opener {
				if !opener.image {
					opener.active = false // deactivate this opener
				}
				opener = opener.previous
			}
		}

		t.context.pos++
		return node
	} else { // no match
		t.removeBracket() // remove this opener from stack
		t.context.pos = startPos
		t.context.pos++
		return &Text{tokens: toItems("]")}
	}
}

func (t *Tree) parseOpenBracket(tokens items) (ret Node) {
	t.context.pos++
	ret = &Text{tokens: toItems("[")}
	// Add entry to stack for this opener
	t.addBracket(ret, t.context.pos, false)

	return
}

func (t *Tree) addBracket(node Node, index int, image bool) {
	if nil != t.context.brackets {
		t.context.brackets.bracketAfter = true
	}

	t.context.brackets = &delimiter{
		node:              node,
		previous:          t.context.brackets,
		previousDelimiter: t.context.delimiters,
		index:             index,
		image:             image,
		active:            true,
	}
}

func (t *Tree) removeBracket() {
	t.context.brackets = t.context.brackets.previous
}

func (t *Tree) parseBackslash(tokens items) (ret Node) {
	if len(tokens)-1 > t.context.pos {
		t.context.pos++
	}
	token := tokens[t.context.pos]
	if itemNewline == token {
		ret = &HardBreak{&BaseNode{typ: NodeHardBreak}}
		t.context.pos++
	} else if isASCIIPunct(token) {
		ret = &Text{tokens: items{token}}
		t.context.pos++
	} else {
		ret = &Text{tokens: toItems("\\")}
	}

	return
}

func (t *Tree) parseText(tokens items) (ret Node) {
	length := len(tokens)
	var token byte
	start := t.context.pos
	for ; t.context.pos < length; t.context.pos++ {
		token = tokens[t.context.pos]
		if t.isMarker(token) {
			// 遇到潜在的标记符时需要跳出 text，回到行级解析主循环
			if start == t.context.pos {
				start++
			}
			break
		}
	}

	ret = &Text{tokens: tokens[start:t.context.pos]}
	return
}

// isMarker 判断 token 是否是潜在的 Markdown 标记。
func (t *Tree) isMarker(token byte) bool {
	return itemAsterisk == token || itemUnderscore == token || itemOpenBracket == token || itemBang == token ||
		itemNewline == token || itemBackslash == token || itemBacktick == token ||
		itemLess == token || itemCloseBracket == token || itemAmpersand == token || itemTilde == token
}

func (t *Tree) parseNewline(block Node, tokens items) (ret Node) {
	t.context.pos++
	// check previous node for trailing spaces
	lastc := block.LastChild()
	hardbreak := false
	if nil != lastc {
		if text, ok := lastc.(*Text); ok {
			tokens := text.tokens
			if valueLen := len(tokens); itemSpace == tokens[valueLen-1] {
				lastc.SetTokens(tokens.trimRight())
				if 1 < valueLen {
					hardbreak = itemSpace == tokens[len(tokens)-2]
				}
			}
		}
	}

	if hardbreak {
		ret = &HardBreak{&BaseNode{typ: NodeHardBreak}}
	} else {
		ret = &SoftBreak{&BaseNode{typ: NodeSoftBreak}}
	}

	return
}
