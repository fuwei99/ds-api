'use strict';

const fs = require('fs');
const path = require('path');

const DEFAULT_CLIENT = Object.freeze({
  name: 'DeepSeek',
  platform: 'web',
  androidApiLevel: '',
  locale: 'zh_CN',
});

const DEFAULT_BASE_HEADERS = Object.freeze({
  Host: 'chat.deepseek.com',
  Accept: 'application/json',
  'Content-Type': 'application/json',
  // 真实网页版抓包确认 platform=web 同样携带此头，早前当作 App 专属移除是误判。
  'x-client-bundle-id': 'com.deepseek.chat',
});

// 必须与 Go 侧 internal/deepseek/transport.ChromeMajorVersion 一致。
// 注意：Node 路径（Vercel）走原生 fetch，拿不到 uTLS，TLS/HTTP2 指纹无法伪装，
// 这里只能保证 HTTP 头自洽。详见 docs/DEPLOY.md 的风险说明。
const CHROME_MAJOR_VERSION = '150';
const CHROME_USER_AGENT = `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${CHROME_MAJOR_VERSION}.0.0.0 Safari/537.36`;
const CHROME_SEC_CH_UA = `"Not;A=Brand";v="8", "Chromium";v="${CHROME_MAJOR_VERSION}", "Google Chrome";v="${CHROME_MAJOR_VERSION}"`;

const WEB_BROWSER_HEADERS = Object.freeze({
  'User-Agent': CHROME_USER_AGENT,
  'sec-ch-ua': CHROME_SEC_CH_UA,
  'sec-ch-ua-mobile': '?0',
  'sec-ch-ua-platform': '"Windows"',
  Origin: 'https://chat.deepseek.com',
  Referer: 'https://chat.deepseek.com/',
  'sec-fetch-site': 'same-origin',
  'sec-fetch-mode': 'cors',
  'sec-fetch-dest': 'empty',
  // 浏览器 fetch 发 */*，不是 application/json。
  Accept: '*/*',
  // Chrome 12x+ 在 fetch/XHR 上会带 priority。
  // 这里不设置 accept-encoding：Node 的 fetch/undici 自己协商并解压，
  // 手动覆盖会让它把压缩后的字节原样交出来。
  priority: 'u=1, i',
});

// locale -> IANA 时区。偏移在调用时实时计算（含夏令时），与 Go 侧一致。
const LOCALE_TIMEZONES = Object.freeze({
  zh_CN: 'Asia/Shanghai',
  zh_TW: 'Asia/Taipei',
  en_US: 'America/Los_Angeles',
  en_GB: 'Europe/London',
  ja_JP: 'Asia/Tokyo',
  ko_KR: 'Asia/Seoul',
  de_DE: 'Europe/Berlin',
  fr_FR: 'Europe/Paris',
  ru_RU: 'Europe/Moscow',
  es_ES: 'Europe/Madrid',
});

const DEFAULT_TIMEZONE_OFFSET = '28800';

// 「只配了母语」的 Chrome 默认形态，与 Go 侧保持一致（见 constants.go 的说明）。
const LOCALE_ACCEPT_LANGUAGES = Object.freeze({
  zh_CN: 'zh-CN,zh;q=0.9',
  zh_TW: 'zh-TW,zh;q=0.9',
  en_US: 'en-US,en;q=0.9',
  en_GB: 'en-GB,en;q=0.9',
  ja_JP: 'ja-JP,ja;q=0.9',
  ko_KR: 'ko-KR,ko;q=0.9',
  de_DE: 'de-DE,de;q=0.9',
  fr_FR: 'fr-FR,fr;q=0.9',
  ru_RU: 'ru-RU,ru;q=0.9',
  es_ES: 'es-ES,es;q=0.9',
});

const DEFAULT_SKIP_PATTERNS = Object.freeze([
  'quasi_status',
  'elapsed_secs',
  'token_usage',
  'pending_fragment',
  'conversation_mode',
  'fragments/-1/status',
  'fragments/-2/status',
  'fragments/-3/status',
]);

const DEFAULT_SKIP_EXACT_PATHS = Object.freeze([
  'response/search_status',
]);

function asNonEmptyString(value) {
  return typeof value === 'string' && value !== '' ? value : '';
}

function normalizeClient(raw) {
  const client = raw && typeof raw === 'object' && !Array.isArray(raw) ? raw : {};
  return {
    name: asNonEmptyString(client.name) || DEFAULT_CLIENT.name,
    platform: asNonEmptyString(client.platform) || DEFAULT_CLIENT.platform,
    version: asNonEmptyString(client.version),
    androidApiLevel: asNonEmptyString(client.android_api_level) || DEFAULT_CLIENT.androidApiLevel,
    locale: asNonEmptyString(client.locale) || DEFAULT_CLIENT.locale,
  };
}

// 返回该 IANA 时区此刻相对 UTC 的偏移秒数（含夏令时）。
// 做法是把同一时刻按目标时区格式化，再当成 UTC 反解，两者之差即偏移。
function zoneOffsetSeconds(zone, now) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    hour12: false,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).formatToParts(now);

  const field = {};
  for (const part of parts) field[part.type] = part.value;
  const asUTC = Date.UTC(
    Number(field.year),
    Number(field.month) - 1,
    Number(field.day),
    Number(field.hour) % 24,
    Number(field.minute),
    Number(field.second),
  );
  // 抹掉毫秒，避免格式化时的秒级截断带来 ±1 秒抖动。
  return Math.round((asUTC - Math.floor(now.getTime() / 1000) * 1000) / 1000);
}

function timezoneOffsetFor(locale) {
  const zone = LOCALE_TIMEZONES[asNonEmptyString(locale)];
  if (!zone) return DEFAULT_TIMEZONE_OFFSET;
  try {
    return String(zoneOffsetSeconds(zone, new Date()));
  } catch {
    return DEFAULT_TIMEZONE_OFFSET;
  }
}

function acceptLanguageFor(locale) {
  const key = asNonEmptyString(locale);
  return key && LOCALE_ACCEPT_LANGUAGES[key] ? LOCALE_ACCEPT_LANGUAGES[key] : 'zh-CN,zh;q=0.9';
}

function isWebPlatform(platform) {
  return asNonEmptyString(platform).toLowerCase() === 'web';
}

function buildBaseHeaders(parsed, client) {
  const rawBaseHeaders = parsed && typeof parsed.base_headers === 'object' && !Array.isArray(parsed.base_headers)
    ? parsed.base_headers
    : {};
  const baseHeaders = { ...DEFAULT_BASE_HEADERS, ...rawBaseHeaders };

  const locale = client.locale || 'zh_CN';
  baseHeaders['x-client-timezone-offset'] = timezoneOffsetFor(locale);

  if (isWebPlatform(client.platform)) {
    Object.assign(baseHeaders, WEB_BROWSER_HEADERS);
    baseHeaders['Accept-Language'] = acceptLanguageFor(locale);
  } else if (client.name && client.version) {
    baseHeaders['User-Agent'] = `${client.name}/${client.version}`;
  }

  if (client.platform) {
    baseHeaders['x-client-platform'] = client.platform;
  }
  if (client.version) {
    baseHeaders['x-client-version'] = client.version;
  }
  if (client.locale) {
    baseHeaders['x-client-locale'] = client.locale;
  }
  return baseHeaders;
}

function sharedConstantsPaths() {
  return [
    path.resolve(__dirname, '../../deepseek/protocol/constants_shared.json'),
    path.resolve(process.cwd(), 'internal/deepseek/protocol/constants_shared.json'),
  ];
}

function readSharedConstants() {
  try {
    return require('../../deepseek/protocol/constants_shared.json');
  } catch (_err) {
    // Fall through to filesystem candidates for test and local execution variants.
  }
  for (const sharedPath of sharedConstantsPaths()) {
    try {
      const raw = fs.readFileSync(sharedPath, 'utf8');
      return JSON.parse(raw);
    } catch (_err) {
      // Try the next candidate path; fall back to in-file structural defaults below.
    }
  }
  return {};
}

function loadSharedConstants() {
  const parsed = readSharedConstants();
  const client = normalizeClient(parsed && parsed.client);
  const skipPatterns = Array.isArray(parsed && parsed.skip_contains_patterns)
    ? parsed.skip_contains_patterns.filter((v) => typeof v === 'string' && v !== '')
    : [...DEFAULT_SKIP_PATTERNS];
  const skipExactPaths = Array.isArray(parsed && parsed.skip_exact_paths)
    ? parsed.skip_exact_paths.filter((v) => typeof v === 'string' && v !== '')
    : [...DEFAULT_SKIP_EXACT_PATHS];
  return {
    client,
    baseHeaders: buildBaseHeaders(parsed, client),
    skipPatterns,
    skipExactPaths,
  };
}

const shared = loadSharedConstants();

module.exports = {
  CLIENT: Object.freeze({ ...shared.client }),
  CLIENT_VERSION: shared.client.version,
  BASE_HEADERS: Object.freeze(shared.baseHeaders),
  SKIP_PATTERNS: Object.freeze(shared.skipPatterns),
  SKIP_EXACT_PATHS: new Set(shared.skipExactPaths),
  WEB_BROWSER_HEADERS: Object.freeze({ ...WEB_BROWSER_HEADERS }),
  CHROME_USER_AGENT,
  CHROME_SEC_CH_UA,
  timezoneOffsetFor,
  acceptLanguageFor,
  isWebPlatform,
  __test: {
    buildBaseHeaders,
    normalizeClient,
    sharedConstantsPaths,
    timezoneOffsetFor,
    acceptLanguageFor,
  },
};
