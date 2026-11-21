import axios, { AxiosError, AxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'

/** 后端统一响应包：{code,message,data}。 */
export interface ApiEnvelope<T> {
  code: number
  message: string
  data: T
}

/** 分页响应体。 */
export interface PageData<T> {
  list: T[]
  total: number
  page: number
  pageSize: number
}

const TOKEN_KEY = 'tmp.token'

/** 读取本地 token。 */
export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || ''
}

/** 写入本地 token。 */
export function setToken(token: string): void {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token)
  } else {
    localStorage.removeItem(TOKEN_KEY)
  }
}

/** 未认证时的回调钩子（由 router 注册，避免循环依赖）。 */
let unauthorizedHandler: (() => void) | null = null

/** 注册未认证（40101）处理器。 */
export function onUnauthorized(fn: () => void): void {
  unauthorizedHandler = fn
}

/** 需要先改初始密码时广播的事件名（MainLayout 监听后强制弹出改密弹窗）。 */
export const MUST_CHANGE_PASSWORD_EVENT = 'tmp:must-change-password'

/** 广播「必须先改初始密码」事件。 */
function notifyMustChangePassword(): void {
  window.dispatchEvent(new CustomEvent(MUST_CHANGE_PASSWORD_EVENT))
}

/** 上一次 40302 提示时间，避免首屏多个并发请求同时弹出多条相同提示。 */
let lastMustChangeToastAt = 0

const instance = axios.create({
  baseURL: '/api/v1',
  timeout: 60000,
})

// 请求拦截：注入 Bearer token 与请求 ID。
instance.interceptors.request.use((config) => {
  const token = getToken()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// 响应拦截：统一错误提示与 40101 处理。
instance.interceptors.response.use(
  (resp) => resp,
  (error: AxiosError<ApiEnvelope<unknown>>) => {
    const status = error.response?.status
    const payload = error.response?.data
    const code = payload?.code
    const message = payload?.message || error.message || '请求失败'

    if (status === 401 || code === 40101) {
      setToken('')
      if (unauthorizedHandler) {
        unauthorizedHandler()
      } else {
        ElMessage.warning(message)
      }
      return Promise.reject(new Error(message))
    }
    // 40302：初始密码未修改，强制回到改密弹窗（不清 token，改完即可继续）。
    if (code === 40302) {
      const now = Date.now()
      if (now - lastMustChangeToastAt > 3000) {
        lastMustChangeToastAt = now
        ElMessage.warning(message || '请先修改初始密码')
      }
      notifyMustChangePassword()
      return Promise.reject(new Error(message))
    }
    // 50201（采集失败）与 40901（能力不支持）不弹全局红条时由页面自行处理，此处统一提示。
    if (status === 403 || code === 40301) {
      ElMessage.error(message || '无权限执行该操作')
    } else if (status === 429 || code === 42901) {
      ElMessage.error(message)
    } else if (code === 40901) {
      ElMessage.warning(message)
    } else if (!error.config?.headers?.['X-TMP-Silent']) {
      ElMessage.error(message)
    }
    return Promise.reject(new Error(message))
  },
)

/** 发起请求并解包 data。 */
export async function request<T>(config: AxiosRequestConfig): Promise<T> {
  const resp = await instance.request<ApiEnvelope<T>>(config)
  return resp.data?.data as T
}

/** 原始响应（用于下载等场景）。 */
export const rawClient = instance

export default instance
