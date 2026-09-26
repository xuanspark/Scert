import { createRenderer, nextTick } from "vue";

export const aliyunProvider = {
  id: "aliyun",
  name: "阿里云 DNS",
  available: true,
  credential_fields: [{ name: "access_key_id", label: "访问密钥", required: true }],
};

export function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((accept, fail) => {
    resolve = accept;
    reject = fail;
  });
  return { promise, resolve, reject };
}

export function verifiedResponse(token = "test-token") {
  return Response.json({
    verified: true,
    credential_ref: "secret://test/only",
    verification_token: token,
    expires_at: new Date(Date.now() + 60000).toISOString(),
  });
}

// 运行真实组合式逻辑和生命周期，不加载浏览器，也不连接 DNS 或业务数据库。
export async function mountComposable(t, factory, fetcher) {
  const windowEvents = new EventTarget();
  const intervals = new Map();
  let nextIntervalID = 1;
  let currentTime = Date.now();
  const originals = new Map();
  for (const [key, value] of Object.entries({
    window: {
      addEventListener: windowEvents.addEventListener.bind(windowEvents),
      removeEventListener: windowEvents.removeEventListener.bind(windowEvents),
      dispatchEvent: windowEvents.dispatchEvent.bind(windowEvents),
      setInterval(callback) {
        const intervalID = nextIntervalID;
        nextIntervalID += 1;
        intervals.set(intervalID, callback);
        return intervalID;
      },
      clearInterval(intervalID) {
        intervals.delete(intervalID);
      },
      location: { pathname: "/", search: "", hash: "", assign() {} },
      history: { replaceState() {} },
      setTimeout,
      clearTimeout,
    },
    document: { body: { style: { overflow: "" } } },
  })) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
  }
  t.mock.method(Date, "now", () => currentTime);
  t.mock.method(globalThis, "fetch", fetcher);
  const renderer = createRenderer({
    createComment: () => ({}),
    insert() {},
    remove() {},
    parentNode: () => null,
    nextSibling: () => null,
  });
  let state;
  const app = renderer.createApp({
    setup() {
      state = factory();
      return () => null;
    },
  });
  app.mount({});
  let mounted = true;
  const unmount = () => {
    if (mounted) app.unmount();
    mounted = false;
  };
  t.after(() => {
    unmount();
    for (const [key, descriptor] of originals) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else delete globalThis[key];
    }
  });
  await new Promise((resolve) => setImmediate(resolve));
  await nextTick();
  return {
    state,
    unmount,
    runIntervals() {
      for (const callback of [...intervals.values()]) callback();
    },
    advanceTime(milliseconds) {
      currentTime += milliseconds;
    },
  };
}
