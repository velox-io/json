package gbind

import (
	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/native/ndec"
)

// skipValue skips a field value at the cursor.
func (c *binder) skipValue() error {
	p, err := c.skipAt(c.txt, c.p)
	c.p = p
	return err
}

// skipAt skips the field value at token start p, leniently under
// BindOptSkipLenient: brackets count without validating scalars or
// commas.
func (c *binder) skipAt(s text, p int) (int, error) {
	if p >= s.n {
		return p, c.fail(ndec.BindErrEOF, uint64(p))
	}
	lenient := c.opt&ndec.BindOptSkipLenient != 0
	if ch := s.at(p); ch == '{' || ch == '[' {
		return c.nestedAt(s, p+1, !lenient)
	}
	if lenient {
		c.p = p
		err := c.skipToken()
		return c.p, err
	}
	return c.scalarAt(s, p)
}

// skipNested moves past the container opening at the cursor without
// validating it.
func (c *binder) skipNested() error {
	p, err := c.nestedAt(c.txt, c.p+1, false)
	c.p = p
	return err
}

// safeSkip validates a skipped scalar, and within a container rejects a
// comma followed by a close or another comma.
func (c *binder) safeSkip() error {
	return c.skipChecked(true)
}

// rootSkip consumes the root value after a recorded mismatch. Scalars
// validate; containers count brackets. depth is one when the opening
// bracket is already consumed.
func (c *binder) rootSkip(depth int) error {
	if depth == 0 {
		return c.skipChecked(false)
	}
	p, err := c.nestedAt(c.txt, c.p, false)
	c.p = p
	return err
}

// skipChecked skips one value whose scalar, if it is one, must validate.
// commas selects the safe skip's comma check inside containers.
func (c *binder) skipChecked(commas bool) error {
	s, p := c.txt, c.p
	var err error
	switch {
	case p >= s.n:
		err = c.fail(ndec.BindErrEOF, uint64(p))
	case s.at(p) == '{' || s.at(p) == '[':
		p, err = c.nestedAt(s, p+1, commas)
	default:
		p, err = c.scalarAt(s, p)
	}
	c.p = p
	return err
}

// nestedAt passes the rest of a container whose opening precedes p and
// returns the token start past its close.
func (c *binder) nestedAt(s text, p int, commas bool) (int, error) {
	end, stop := s.skipNested(p, c.strict, commas)
	switch stop {
	case stopUnclosed, stopBody:
		return end, c.failNoPos(ndec.BindErrSyntax)
	case stopEOF:
		return end, c.fail(ndec.BindErrEOF, uint64(end))
	case stopComma:
		return end, c.fail(ndec.BindErrSyntax, uint64(end))
	}
	return s.skip(end), nil
}

// scalarAt validates the scalar at token start p < n and returns the token
// start past it.
func (c *binder) scalarAt(s text, p int) (int, error) {
	end, ok := p, false
	switch ch := s.at(p); {
	case ch == '"':
		end, ok = gdec.ScanString(c.src, p+1)
		if ok && c.strict && !s.bodyOK(p+1, end) {
			return p, c.failNoPos(ndec.BindErrSyntax)
		}
		end++
	case ch == 't' || ch == 'f' || ch == 'n':
		end, ok = s.atomEnd(p, ch)
	case ch == '-' || gdec.IsDigit(ch):
		end, ok = gdec.ValidNumber(c.src, p)
	}
	if !ok {
		return p, c.fail(ndec.BindErrSyntax, uint64(p))
	}
	return s.skip(end), nil
}

// validValue consumes one value, checking the full grammar with errors at
// token starts. Its containers count toward the bind depth, as the native
// Value walk shares the bind frames.
func (c *binder) validValue() error {
	if c.eof() {
		return c.fail(ndec.BindErrEOF, c.pos())
	}
	ch := c.peek()
	if ch != '{' && ch != '[' {
		p, err := c.scalarAt(c.txt, c.p)
		c.p = p
		return err
	}
	if err := c.push(); err != nil {
		return err
	}
	c.next()
	closer := byte(']')
	if ch == '{' {
		closer = '}'
	}
	if c.accept(closer) {
		c.pop()
		return nil
	}
	for {
		if ch == '{' {
			if c.eof() {
				return c.fail(ndec.BindErrEOF, c.pos())
			}
			kpos := c.pos()
			if c.peek() != '"' {
				return c.fail(ndec.BindErrSyntax, kpos)
			}
			end, ok := gdec.ScanString(c.src, c.p+1)
			if !ok {
				return c.fail(ndec.BindErrSyntax, kpos)
			}
			if c.strict && !gdec.ValidateBody(c.src, c.p+1, end) {
				return c.failNoPos(ndec.BindErrSyntax)
			}
			c.to(end + 1)
			if c.peek() != ':' {
				return c.fail(ndec.BindErrSyntax, c.pos())
			}
			c.next()
		}
		if err := c.validValue(); err != nil {
			return err
		}
		switch c.peek() {
		case ',':
			c.next()
		case closer:
			c.next()
			c.pop()
			return nil
		default:
			return c.closeErr()
		}
	}
}
