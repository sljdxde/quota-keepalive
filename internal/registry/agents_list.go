package registry

import (
	"context"
	"encoding/json"
)

// AgentsURL 是面板/桥接加载自描述视图的内部地址。
const AgentsURL = "qka://agents"

// AgentsList 返回 agents_list 工具的 JSON 结果（自描述对象数组）。
func (r *Registry) AgentsList(ctx context.Context) ([]byte, error) {
	list, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []AgentDescriptor{}
	}
	return json.Marshal(list)
}
