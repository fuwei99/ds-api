// 凭据开关（internal/config/credential_flags.go）由服务端注册表声明，并通过
// /admin/config 的 credential_flag_specs 下发给前端。账号页据此为每个开关泛化
// 渲染一个开关控件与一枚列表徽标——新增一个按 Key 的开关无需改这里的组件。
//
// 这里集中处理「未显式设置的开关读作声明默认值」这条规则：接口只为非默认值
// 下发字段（配置文件同样只落非默认值），所以默认开启的开关必须回退到 spec.default，
// 否则界面会把一个实际生效的开关画成关闭。

export function credentialFlagSpecs(config) {
    const specs = config?.credential_flag_specs
    if (!Array.isArray(specs)) {
        return []
    }
    return specs.filter((spec) => spec && spec.name)
}

// credentialFlagValue 返回某个 Key 上该开关的生效值。
export function credentialFlagValue(item, spec) {
    const raw = item?.[spec?.name]
    return raw === undefined || raw === null ? Boolean(spec?.default) : Boolean(raw)
}

// credentialFlagFormValues 把一条 Key 记录摊成表单初值。
export function credentialFlagFormValues(config, item) {
    const values = {}
    for (const spec of credentialFlagSpecs(config)) {
        values[spec.name] = credentialFlagValue(item, spec)
    }
    return values
}

// credentialFlagPayload 把表单里的开关摊回请求体，供新增/更新接口按名字读取。
export function credentialFlagPayload(config, values) {
    const payload = {}
    for (const spec of credentialFlagSpecs(config)) {
        payload[spec.name] = Boolean(values?.[spec.name])
    }
    return payload
}
