/** 添加向导的登录方式页签。 */
export type AddAccountTab = 'browser' | 'pat' | 'import'

/** 向导步骤：先选账号类型（method），再进入登录（login）。 */
export type AddAccountStep = 'method' | 'login'

/** 向导阶段：busy = 创建 / 启动请求在途；polling = 等待浏览器授权（可取消）。 */
export type AddAccountPhase = 'idle' | 'busy' | 'polling' | 'done'
