package chat

import (
	"fmt"
	"net/http"

	"app/provider/openai"

	"github.com/sohaha/zlsgo/zerror"
	"github.com/sohaha/zlsgo/zjson"
	"github.com/sohaha/zlsgo/zlog"
	"github.com/sohaha/zlsgo/znet"
	"github.com/sohaha/zlsgo/zstring"
	"github.com/sohaha/zlsgo/zutil"
)

func (h *Index) ANY(c *znet.Context) error {
	data, _ := c.GetDataRawBytes()
	if len(data) == 0 {
		data = zstring.String2Bytes(c.DefaultFormOrQuery("text", ""))
	}

	return h.chat(c, data)
}

func (h *Index) chat(c *znet.Context, data []byte) (err error) {
	if len(data) == 0 {
		return zerror.InvalidInput.Text("对话内容不能为空")
	}

	r := h.mu.RLock()
	defer h.mu.RUnlock(r)

	var (
		nodesKeys []string
		lastErr   error
		nodes     = h.pool
		ctx       = c.Request.Context()
		nodeName  = "unknown"
		stream    = zjson.GetBytes(data, "stream").Bool()
		model     = zjson.GetBytes(data, "model").String()
	)

	defer func() {
		i, ok, _ := h.total.ProvideGet(nodeName, func() (*zutil.Int64, bool) {
			return zutil.NewInt64(0), true
		})
		if ok && i != nil {
			i.Add(1)
		}
	}()

	if model == "" {
		err = nodes.Run(func(node openai.Openai) (normal bool, err error) {
			if ctx.Err() != nil {
				return true, ctx.Err()
			}

			defer func() {
				lastErr = err
			}()

			nodeName = node.Name()

			zlog.Tips("Node", nodeName, "--", nodes.Keys())
			normal, err = h.node(c, stream, node, data)
			return
		})
	} else {
		nodesKeys = h.modelMaps[model]
		if len(nodesKeys) == 0 {
			return zerror.InvalidInput.Text(fmt.Sprintf("model %s not supported", model))
		}
		err = nodes.RunByKeys(nodesKeys, func(node openai.Openai) (normal bool, err error) {
			if ctx.Err() != nil {
				return true, ctx.Err()
			}

			defer func() {
				lastErr = err
			}()

			nodeName = node.Name()
			zlog.Tips("Node", nodeName, "--", nodesKeys)
			normal, err = h.node(c, stream, node, data)
			return
		}, conf.Balancer)
	}

	if lastErr != nil {
		return lastErr
	}

	return err
}

func (h *Index) node(c *znet.Context, stream bool, node openai.Openai, data []byte) (normal bool, err error) {
	if stream {
		sse := znet.NewSSE(c)
		done, err := node.Stream(c.Request.Context(), data, func(data string, raw []byte) {
			sse.SendByte("", raw)
		})
		if err != nil {
			return false, err
		}

		go func() {
			<-done
			sse.Stop()
		}()

		sse.Push()

		return true, err
	}

	resp, err := node.Generate(c.Request.Context(), data)
	if err != nil {
		return false, err
	}

	if !zjson.ValidBytes(data) {
		c.String(http.StatusOK, resp.Get("choices.0.message.content").String())
		return true, nil
	}

	c.Byte(http.StatusOK, resp.Bytes())
	c.SetContentType("application/json")

	return true, nil
}
