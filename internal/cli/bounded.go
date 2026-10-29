package cli

import (
	"bytes"
	"fmt"
)

// ErrOutputOverflow 在子进程输出超过硬上限时返回。
type ErrOutputOverflow struct {
	Limit int
}

func (e ErrOutputOverflow) Error() string {
	return fmt.Sprintf("子进程输出超过硬上限 %d 字节", e.Limit)
}

// BoundedBuffer 累积字节，一旦超过上限即显式失败。
type BoundedBuffer struct {
	limit int
	buf   bytes.Buffer
}

// NewBoundedBuffer 创建容量为 limit 的有界缓冲。
func NewBoundedBuffer(limit int) *BoundedBuffer {
	return &BoundedBuffer{limit: limit}
}

func (b *BoundedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > b.limit {
		return 0, ErrOutputOverflow{Limit: b.limit}
	}
	return b.buf.Write(p)
}

func (b *BoundedBuffer) String() string { return b.buf.String() }
func (b *BoundedBuffer) Len() int       { return b.buf.Len() }
