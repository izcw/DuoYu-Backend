import { reactive } from "vue";
export const session = reactive({
  ready: false,
  authenticated: false,
  csrf: "",
  error: "",
  profile: { name: "", picture: "" },
});
export async function api(
  path: string,
  body?: unknown,
  method = body === undefined ? "GET" : "POST",
) {
  const response = await fetch("/api/admin/" + path, {
    method,
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": session.csrf,
    },
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
  });
  const data = await response.json();
  if (!response.ok) {
    if (response.status === 401) session.authenticated = false;
    throw Error(data.error || "请求失败");
  }
  return data;
}
export async function loadSession() {
  try {
    const me = await api("me");
    session.csrf = me.csrf;
    session.profile = me.profile || { name: "", picture: "" };
    session.authenticated = true;
    session.error = "";
  } catch (e) {
    session.error = e instanceof Error ? e.message : "服务暂不可用";
  } finally {
    session.ready = true;
  }
}
export const size = (n: unknown) => {
  const v = Number(n || 0);
  return v >= 1048576
    ? (v / 1048576).toFixed(1) + " MB"
    : v >= 1024
      ? (v / 1024).toFixed(1) + " KB"
      : v + " B";
};
export const date = (v: unknown) =>
  v ? new Date(String(v)).toLocaleString("zh-CN", { hour12: false }) : "—";
