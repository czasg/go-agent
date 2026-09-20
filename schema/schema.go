package schema

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/invopop/jsonschema"
)

// Parameters 通过反射把一个 Go struct 解析成 JSON Schema（map[string]any，
// 与 ToolInfo.Parameters 一致），省去手写 schema 的重复与易错。
//
// 实现参考 eino 的反射机制：用 invopop/jsonschema 的 Reflector 做匿名反射，
// 并禁用 $ref/$defs，得到最朴素的 {type, properties, required} 结构。
//
// struct tag 约定（与 eino 一致）：
//   - json:"name"                  参数名（模型可见）；加 ,omitempty 表示非必填
//   - jsonschema:"description=..."  参数描述
//   - jsonschema:"required"         强制必填（默认非 omitempty 即必填，可不写）
//   - jsonschema:"enum=a,enum=b"    枚举值（逗号分隔）
//   - jsonschema:"default=x"        默认值
//
// 注意：invopop/jsonschema 会给 object 自动补 additionalProperties:false，
// 这是 OpenAI strict function calling 所要求的，保留即可。
//
// v 必须是 struct（或 struct 指针），否则返回错误。
func Parameters(v any) (map[string]any, error) {
	if v == nil {
		return nil, fmt.Errorf("schema: 入参为 nil")
	}

	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("schema: 入参必须是 struct，实际是 %s", t.Kind())
	}

	r := &jsonschema.Reflector{
		Anonymous:      true, // 去掉 $id
		DoNotReference: true, // 不用 $ref/$defs，直接内联
	}

	js := r.Reflect(v)
	js.Version = "" // 去掉 $schema 声明，只留参数结构

	data, err := json.Marshal(js)
	if err != nil {
		return nil, fmt.Errorf("schema: 序列化 JSON Schema 失败: %w", err)
	}

	out := make(map[string]any)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("schema: 反序列化 JSON Schema 失败: %w", err)
	}
	return out, nil
}

// MustParameters 是 Parameters 的 panic 版。入参非 struct 属于编程错误，
// 适合在工具构造期直接调用，早崩早暴露。
func MustParameters(v any) map[string]any {
	p, err := Parameters(v)
	if err != nil {
		panic(err)
	}
	return p
}

// InferToolInfo 从入参 struct 反射生成参数 schema，并组装成完整 ToolInfo。
//
// 建议工具在构造时调用一次并把结果缓存进 struct：agent 每轮都会调用 Info()
// 收集工具列表（见 ToolStore.ListToolInfos），反射有开销，不应每次都跑。
func InferToolInfo(name, desc string, params any) (ToolInfo, error) {
	p, err := Parameters(params)
	if err != nil {
		return ToolInfo{}, err
	}
	return ToolInfo{Name: name, Description: desc, Parameters: p}, nil
}

// MustInferToolInfo 是 InferToolInfo 的 panic 版，让工具构造函数无需返回 error。
func MustInferToolInfo(name, desc string, params any) ToolInfo {
	ti, err := InferToolInfo(name, desc, params)
	if err != nil {
		panic(err)
	}
	return ti
}
