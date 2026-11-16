import "./site.css";

type Theme = "light" | "dark";
type Platform = "windows-x64" | "macos-arm64-dmg";
type Artifact = { filename: string; size_bytes: number; download_url: string };
type Release = {
  version: string;
  notes?: string;
  platforms: Partial<Record<Platform, Artifact>>;
};
type Scene = {
  name: string;
  role: string;
  headline: string;
  description: string;
  benefit: string;
  tasks: string[];
  ask: string;
  reply: string;
  work: string;
  files: string[];
  result: string;
};
const scenes: Scene[] = [
  {
    name: "独立站团队",
    role: "增长研究员",
    headline: "让每一次上新，\n都有更充分的准备。",
    description:
      "把评论、竞品和商品资料变成购买理由，再交给内容与设计机器人接着做。运营、设计、投放围绕同一份研究推进。",
    benefit: "少重复调研，少来回沟通，让团队更快测试能打动顾客的卖点。",
    tasks: [
      "研究机器人：归纳用户顾虑与购买动机",
      "内容机器人：完成商品页和广告文案",
      "设计机器人：整理主图方向与补拍清单",
    ],
    ask: "分析这 60 条商品评价，整理购买动机，再准备商品页文案和素材清单。",
    reply: "我会先合并重复反馈，找到用户最在意的卖点，再交给内容和设计机器人。",
    work: "正在整理评论与竞品信息",
    files: ["用户洞察与来源.docx", "商品页文案.docx", "素材拍摄清单.xlsx"],
    result: "研究、文案和素材需求已经整理好，团队可以直接开始下一步。",
  },
  {
    name: "TikTok Shop",
    role: "广告创意助手",
    headline: "从一个商品卖点，\n到多种广告创意。",
    description:
      "围绕产品卖点和目标人群，准备不同 Hook、脚本和分镜，生成画面、配音与音乐，再剪成可供团队评审的短视频。",
    benefit: "把时间花在测试创意上，让每次投放都有更多角度可以选。",
    tasks: [
      "研究机器人：提炼受众痛点与使用场景",
      "创意机器人：写出三种开场与分镜",
      "视频机器人：制作画面、字幕与配乐",
    ],
    ask: "给这款便携榨汁杯做 3 条广告视频，分别突出通勤、健身和露营场景。",
    reply:
      "先做三种开场：早八早餐、训练后补给、周末轻露营。每条都包含产品演示和行动引导。",
    work: "正在准备脚本、分镜与视频素材",
    files: ["三版广告脚本.docx", "分镜与素材清单.xlsx", "广告视频草稿.mp4"],
    result: "三种创意方向已经就绪，字幕和配乐已加入，等你挑选和调整。",
  },
  {
    name: "自媒体工作室",
    role: "内容研究员",
    headline: "不再从空白开始，\n把好题材做成内容。",
    description:
      "持续关注行业动态与受众反馈，筛选有价值的题材，附上来源和素材，再接着写稿、制作画面与音乐、自动剪辑。",
    benefit: "减少搜集和整理的时间，让创作者专注角度、判断与表达。",
    tasks: [
      "研究机器人：搜集信息，核对引用来源",
      "编辑机器人：整理选题、标题与脚本",
      "制作机器人：准备素材并剪辑视频",
    ],
    ask: "追踪本周 AI 创作工具动态，找 5 个选题，整理来源，并做一条 60 秒视频草稿。",
    reply:
      "我会核对官方更新和创作者反馈，合并重复信息，优先选择有新变化、能讲清楚的题材。",
    work: "正在核对来源并整理选题",
    files: ["本周选题与来源.docx", "脚本与素材清单.xlsx", "60秒视频草稿.mp4"],
    result: "5 个选题、原始来源和视频草稿已经整理好，工作室可以接着创作。",
  },
];
const isDownload = location.pathname.replace(/\/$/, "") === "/download";
const icon = (name: string, extra = "") =>
  `<img class="icon ${extra}" src="/marketing-icons/${name}.svg" alt="" aria-hidden="true" />`;
const mascot = (
  state = "idle",
  extra = "",
  body = "circle",
  color = "yellow",
) =>
  `<span class="mascot ${extra}" data-mascot="${state}" data-body="${body}" data-color="${color}" aria-hidden="true"></span>`;
const botLooks = [
  ["circle", "yellow"],
  ["squircle", "cyan"],
  ["blob", "pink"],
] as const;
function heroWordmark() {
  const letters = [..."DoneBot"]
    .map(
      (letter, i) =>
        `<span class="letter-arrival" style="--i:${i}"><span class="hero-letter">${letter}</span></span>`,
    )
    .join("");
  const flyers = [
    ["playful", "circle", "yellow"],
    ["thinking", "squircle", "cyan"],
    ["happy", "blob", "pink"],
    ["working", "capsule", "orange"],
    ["curious", "hexagon", "teal"],
    ["proud", "star", "chrome"],
  ]
    .map(
      ([state, body, color], i) =>
        `<span class="hero-flyer" style="--i:${i}">${mascot(state, "", body, color)}</span>`,
    )
    .join("");
  return `<div class="hero-wordmark" aria-hidden="true"><span class="wordmark-ghost ghost-top">DoneBot</span><span class="wordmark-main">${letters}</span><span class="wordmark-ghost ghost-bottom">DoneBot</span>${flyers}</div>`;
}
const brand = () =>
  `<a class="brand" href="/" aria-label="DoneBot 首页"><img src="/donebot-logo.png" width="29" height="29" alt="" /><span>DoneBot</span></a>`;
const theme = (): Theme =>
  document.documentElement.dataset.theme === "dark" ? "dark" : "light";

function header() {
  return `<a class="skip-link" href="#main">跳到内容</a><header class="site-header"><div class="header-inner">${brand()}<nav aria-label="主导航"><a href="/#features">产品能力</a><a href="/#scenarios">使用场景</a><a href="/#cloud">云端与手机</a></nav><div class="header-actions"><a href="/download" class="button small primary">下载 DoneBot ${icon("arrow-up-right")}</a><button class="theme-toggle" type="button" aria-label="切换到${theme() === "dark" ? "浅" : "深"}色模式" title="切换主题">${icon("sun", "sun-icon")}${icon("moon", "moon-icon")}</button></div></div></header>`;
}
function footer() {
  return `<footer class="site-footer"><div class="footer-inner">${brand()}<p>让任务走到完成。</p><a href="/download">下载客户端 ${icon("arrow-up-right")}</a><span>© ${new Date().getFullYear()} DoneBot</span></div></footer>`;
}
function demo(phone = false) {
  const scene = scenes[phone ? 2 : 0]!;
  return `<div class="demo ${phone ? "phone-demo" : "desktop-demo"}" data-demo="${phone ? "scenario" : "hero"}" data-step="0" role="img" aria-label="DoneBot 自动任务演示：从研究、内容到文件交付">
    ${phone ? '<div class="phone-island"></div>' : `<aside class="demo-sidebar"><div class="workspace-label">${icon("cloud")} 我的机器人团队</div><div class="sidebar-label">机器人 <span>7×24 在线</span></div>${scenes.map((s, i) => `<div class="bot-row ${i === 0 ? "active" : ""}" data-bot-row="${i}">${mascot(i === 0 ? "thinking" : "idle", "", ...botLooks[i]!)}<div><strong>${s.role}</strong><small>${["正在处理任务", "准备创意方向", "持续关注新动态"][i]}</small></div><span class="online-dot"></span></div>`).join("")}<div class="sidebar-note">交代目标，接收结果。<br />让机器人各司其职。</div></aside>`}
    <div class="demo-main"><div class="demo-top">${mascot("thinking", "", ...botLooks[phone ? 2 : 0]!)}<div><strong data-demo-role>${scene.role}</strong><span>DoneBot · <span data-demo-status>正在执行</span></span></div></div>
    <div class="demo-conversation"><div class="conversation-label">${phone ? "今天 · 云端工作空间" : "今天的任务"}</div><div class="demo-message user-message" data-demo-ask>${scene.ask}</div><div class="demo-message bot-message stage-one" data-demo-reply>${scene.reply}</div><div class="work-state stage-two">${mascot("working", "", ...botLooks[phone ? 2 : 0]!)}<span data-demo-work>${scene.work}</span><span class="typing-dots"><i></i><i></i><i></i></span></div><div class="deliverables stage-three"><span class="delivery-label">${icon("check")} 已整理交付</span><div data-demo-files>${scene.files.map((file) => `<div class="file-row">${icon(file.endsWith("mp4") ? "play" : "file-text")}<span>${file}</span>${icon("check")}</div>`).join("")}</div><p data-demo-result>${scene.result}</p></div></div><div class="demo-bottom">${icon("shield-check")} 按你的授权执行，关键动作由你决定</div></div></div>`;
}
function home() {
  return `<main id="main">
    <section class="hero container">
      ${heroWordmark()}
      <div class="hero-copy"><p class="eyebrow"><span class="online-dot"></span> 你的 7×24 机器人团队</p><h1>让<span class="rotating-word" data-rotating-word>日常工作</span>，自动向前。</h1><p class="hero-description">会研究、能创作、懂协作。<br class="mobile-only" />从一句交代，到一份能用的结果。<br class="desktop-only" />把重复的工作交给 DoneBot，把时间留给更重要的事。</p><div class="hero-actions"><a class="button primary" href="/download">${icon("download")} 下载 DoneBot</a><a class="button secondary" href="#demo">看看它怎么工作 ${icon("arrow-right")}</a></div><p class="platform-note">${icon("apple")} macOS Apple 芯片 <span>·</span> ${icon("windows")} Windows x64</p></div>
      <div id="demo" class="hero-demo">${demo()}<p class="demo-caption">你定方向，机器人把工作接着做。</p></div>
    </section>
    <section class="section container getting-started"><div class="section-heading"><p class="eyebrow">从第一句话开始</p><h2>多一位同事，<br class="mobile-only" />少一些琐事。</h2><p>不用先学一套复杂流程。像交代同事一样，把目标告诉它。</p></div><div class="steps-grid">${[
      ["download", "下载并登录", "安装 DoneBot 客户端，登录你的账号。"],
      ["bot", "组建机器人团队", "给研究、运营、创作安排合适的机器人。"],
      ["mail", "告诉它你的目标", "说清想要的结果，补充必要的背景与文件。"],
      ["check", "把结果拿来用", "接收文档、素材和成品，继续追问或调整。"],
    ]
      .map(
        ([symbol, title, desc], i) =>
          `<article class="step-card"><div class="step-top"><span>0${i + 1}</span>${icon(symbol!)}</div><h3>${title}</h3><p>${desc}</p></article>`,
      )
      .join("")}</div></section>
    <section class="section container" id="features"><div class="section-heading"><p class="eyebrow">能做的，比你想的更多</p><h2>从信息，到成品。<br />工作在这里连起来。</h2><p>文档、图片、视频、音乐，还有你每天使用的应用。<br class="desktop-only" />让一个目标带动一整段工作。</p></div>
      <div class="feature-grid"><article class="feature-card feature-wide"><div class="feature-copy"><span class="feature-icon">${icon("search")}</span><h3>找得到信息，<br />也整理得出结论。</h3><p>搜索网页、阅读文件、比对资料。把散落的信息变成有来源的报告、表格和下一步行动。</p><span class="benefit">少找一遍资料，多做一步判断。</span></div><div class="research-art" aria-hidden="true"><div class="research-source"><span>商品评价</span><span>竞品资料</span><span>行业动态</span></div><div class="document-art"><div class="document-top">${icon("file-text")} 用户洞察报告 <span>已完成</span></div><h4>从用户反馈，找到增长机会。</h4><div class="document-metric"><strong>60</strong><span>条评价已归纳</span><strong>3</strong><span>个核心购买理由</span></div><div class="document-item">${icon("check")} 购买动机与常见顾虑</div><div class="document-item">${icon("check")} 页面文案与素材建议</div><div class="document-item">${icon("check")} 原始来源与引用记录</div></div>${mascot("writing", "research-mascot", "squircle", "cyan")}</div></article>
      <article class="feature-card media-card"><div class="media-art image-art" aria-hidden="true"><div class="image-poster"><span>NEW<br />PERSPECTIVE.</span><div class="poster-orbit"></div><small>为你的品牌，换个新视角。</small></div><div class="image-swatch"><i></i><i></i><i></i><span>品牌素材</span></div></div><div class="feature-copy"><h3>${icon("image")} 图片与设计</h3><p>商品主图、内容封面、广告素材。把卖点变成看得见的表达。</p></div></article>
      <article class="feature-card media-card"><div class="media-art video-art" aria-hidden="true"><div class="video-frame"><span>通勤路上，<br />也有新鲜好状态。</span><div class="play-badge">${icon("play")}</div><small>00:18 / 00:30</small></div><div class="video-tracks"><span>画面</span><i></i><i></i><i></i><span>字幕 · 配乐</span></div></div><div class="feature-copy"><h3>${icon("play")} 视频与剪辑</h3><p>脚本、分镜、画面、字幕到自动剪辑，让创意更快进入评审。</p></div></article>
      <article class="feature-card media-card"><div class="media-art music-art" aria-hidden="true"><div class="music-disc">${icon("music")}</div><div class="waveform">${Array.from({ length: 26 }, (_, i) => `<i style="--bar:${18 + ((i * 31) % 53)}px;--delay:${i * 0.07}s"></i>`).join("")}</div><span>让每个画面，都有情绪。</span></div><div class="feature-copy"><h3>${icon("music")} 音乐与声音</h3><p>为短视频、品牌和故事创作配乐或歌曲，让表达完整发生。</p></div></article></div>
    </section>
    <section class="section container" id="scenarios"><div class="section-heading"><p class="eyebrow">放进你的业务里</p><h2>不只是功能。<br />是团队每天少一点忙乱。</h2><p>从研究、创作到交付，让机器人接住那些反复发生的工作。</p></div><div class="scenarios-layout"><div class="scenario-content"><div class="scene-tabs" role="tablist" aria-label="选择使用场景">${scenes.map((scene, i) => `<button type="button" role="tab" id="scene-tab-${i}" aria-controls="scene-panel" aria-selected="${i === 0}" tabindex="${i === 0 ? 0 : -1}" data-scene="${i}">${scene.name}</button>`).join("")}</div><div id="scene-panel" role="tabpanel" aria-labelledby="scene-tab-0"><h3 data-scene-headline></h3><p class="scene-description" data-scene-description></p><div class="handoff" data-scene-tasks></div><div class="scene-benefit">${icon("zap")}<p data-scene-benefit></p></div></div></div><div class="phone-wrap">${demo(true)}</div></div></section>
    <section class="section container" id="cloud"><div class="cloud-card"><div class="cloud-copy"><p class="eyebrow">电脑可以休息，云端团队继续。</p><h2>7×24<span>一直有进展。</span></h2><p>把需要持续在线的机器人放在 Cloud 工作空间。你离开电脑后，它们仍在搜集信息、跟踪变化、推进任务。</p><p>手机随时查看进展、补充方向。回到工作时，先看结果，再做决定。</p><div class="cloud-tags"><span>${icon("cloud")} 云端持续运行</span><span>${icon("smartphone")} 手机随时查看</span></div></div><div class="night-shift"><div class="shift-label"><span>云端团队的一晚</span><span class="online-dot"></span> 持续在线</div><div class="shift-row"><time>22:00</time>${mascot("thinking")}<div><strong>研究机器人</strong><span>追踪行业动态，整理可靠来源</span></div></div><div class="shift-row"><time>02:30</time>${mascot("writing", "", "blob", "pink")}<div><strong>内容机器人</strong><span>将新信息整理成选题与脚本</span></div></div><div class="shift-row"><time>07:00</time>${mascot("working", "", "capsule", "orange")}<div><strong>运营机器人</strong><span>汇总进展，准备今日工作简报</span></div></div><div class="morning-result">${icon("check")} 早上好，新的工作简报已准备好。</div></div></div></section>
    <section class="section container apps-section"><div class="section-heading"><p class="eyebrow">应用连接，也是机器人的能力</p><h2>1,000+ 个应用，<br />接进你的工作。</h2><p>资料在文档，订单在商店，讨论在团队工具里。<br class="desktop-only" />按需授权，让机器人在你已有的工具之间完成工作。</p></div><div class="app-chips" aria-label="可连接的应用示例">${["Shopify", "Google Drive", "Notion", "Slack", "Gmail", "TikTok", "Google Sheets", "WordPress", "Discord", "GitHub", "Outlook", "Zapier"].map((name, i) => `<span>${icon(["layers", "file-text", "file-text", "mail", "mail", "play", "file-text", "globe", "mail", "layers", "mail", "zap"][i]!)}${name}</span>`).join("")}</div><p class="small-note">根据所用应用安装相应能力，并完成必要授权。</p></section>
    <section class="section container faq-section"><div class="section-heading"><p class="eyebrow">还有几个你可能想问的</p><h2>先了解，再开始。</h2></div><div class="faq-list">${[
      [
        "DoneBot 和普通 AI 聊天有什么不同？",
        "除了回答问题，DoneBot 还能读取文件、研究信息、创作多媒体、调用应用，并由不同角色的机器人协作完成任务。你可以继续补充要求，让它沿着目标往下做。",
      ],
      [
        "本地电脑关机后，机器人还会工作吗？",
        "本地工作空间的机器人需要电脑保持运行。Cloud 工作空间中的机器人在云端独立运行，可以 7×24 工作；电脑关机不影响云端机器人。",
      ],
      [
        "我可以从手机查看吗？",
        "可以。通过手机连接相应的工作空间，查看机器人进展、补充信息或确认关键操作。本地工作空间仍需要电脑在线，Cloud 工作空间不依赖本地电脑运行。",
      ],
      [
        "图片、视频、音乐需要切换到别的软件吗？",
        "你可以直接在任务里要求机器人制作图片、视频和音乐，再结合文档、脚本与素材继续处理。需要使用其他应用时，可以按需安装对应能力并授权。",
      ],
      [
        "它会未经确认就执行操作吗？",
        "DoneBot 按你授予的权限执行任务。你可以设置机器人和工具的使用范围；需要人工决定的关键步骤，可以在对话中确认。",
      ],
      [
        "支持什么系统？怎样升级？",
        "目前支持 Windows x64 和搭载 Apple 芯片的 macOS。安装后通过客户端检查更新即可获取新版，官网也会提供最新已发布版本。",
      ],
    ]
      .map(
        ([q, a]) =>
          `<details><summary>${q}${icon("chevron-down")}</summary><p>${a}</p></details>`,
      )
      .join("")}</div></section>
    <section class="final-cta container">${mascot("happy", "cta-mascot")}<p class="eyebrow">你的下一位同事，准备好了。</p><h2>把工作交给 DoneBot。<br />把时间，还给你。</h2><a class="button primary" href="/download">${icon("download")} 下载 DoneBot</a><p class="small-note">macOS Apple 芯片 · Windows x64</p></section>
  </main>`;
}
function download() {
  return `<main id="main" class="download-page container"><div class="download-heading"><img src="/donebot-logo.png" width="64" height="64" alt="DoneBot" /><h1>下载 DoneBot</h1><p>你的机器人团队，从这里开始。</p><span class="release-status" role="status" data-release-status>正在获取最新版本…</span></div><div class="download-grid">${[
    [
      "macos-arm64-dmg",
      "apple",
      "macOS",
      "Apple 芯片 · M 系列",
      "下载 DMG",
      "打开 DMG，将 DoneBot 拖入「应用程序」。",
    ],
    [
      "windows-x64",
      "windows",
      "Windows",
      "x64 · 64 位",
      "下载 EXE",
      "打开安装程序，按提示完成安装。",
    ],
  ]
    .map(
      ([platform, symbol, title, description, label, hint]) =>
        `<article class="download-card">${icon(symbol!, "platform-logo")}<h2>${title}</h2><p>${description}</p><a class="button primary is-disabled" data-download-platform="${platform}" aria-disabled="true" tabindex="-1">${icon("download")} ${label}</a><span class="package-meta" data-package-meta="${platform}">正在检查安装包</span><p class="install-hint">${hint}</p></article>`,
    )
    .join(
      "",
    )}</div><p class="download-footnote">已安装 DoneBot？在客户端检查更新即可。</p><button class="retry-release" type="button" hidden>重新获取版本</button></main>`;
}
const root = document.querySelector<HTMLDivElement>("#donebot-site")!;
root.innerHTML = header() + (isDownload ? download() : home()) + footer();
const mascotRenderer = isDownload ? null : import("./mascot/mount");
void mascotRenderer?.then((module) => module.mountMascots());

function setTheme(value: Theme) {
  document.documentElement.dataset.theme = value;
  try {
    localStorage.setItem("donebot-site-theme", value);
  } catch {
    /* Browser storage may be unavailable in private sessions. */
  }
  document
    .querySelector<HTMLMetaElement>('meta[name="theme-color"]')
    ?.setAttribute("content", value === "dark" ? "#000000" : "#ffffff");
  document
    .querySelector(".theme-toggle")
    ?.setAttribute(
      "aria-label",
      `切换到${value === "dark" ? "浅" : "深"}色模式`,
    );
}
document
  .querySelector(".theme-toggle")
  ?.addEventListener("click", () =>
    setTheme(theme() === "dark" ? "light" : "dark"),
  );

function setDemoScene(element: HTMLElement, index: number) {
  const scene = scenes[index]!;
  const fields: Record<string, string> = {
    role: scene.role,
    ask: scene.ask,
    reply: scene.reply,
    work: scene.work,
    result: scene.result,
  };
  Object.entries(fields).forEach(([name, value]) => {
    const el = element.querySelector(`[data-demo-${name}]`);
    if (el) el.textContent = value;
  });
  const files = element.querySelector("[data-demo-files]");
  if (files)
    files.innerHTML = scene.files
      .map(
        (file) =>
          `<div class="file-row">${icon(file.endsWith("mp4") ? "play" : "file-text")}<span>${file}</span>${icon("check")}</div>`,
      )
      .join("");
  element
    .querySelectorAll("[data-bot-row]")
    .forEach((row, i) => row.classList.toggle("active", i === index));
  element.dataset.step = "0";
  const [body, color] = botLooks[index]!;
  element
    .querySelectorAll<HTMLElement>(
      ".demo-top [data-mascot], .work-state [data-mascot]",
    )
    .forEach((avatar) => {
      avatar.dataset.body = body;
      avatar.dataset.color = color;
    });
  void mascotRenderer?.then((module) => module.mountMascots(element));
}
let currentScene = 0;
let heroScene = 0;
let tick = 0;
let scenarioTick = 0;
function selectScene(index: number) {
  currentScene = index;
  scenarioTick = 0;
  const scene = scenes[index]!;
  document
    .querySelectorAll<HTMLButtonElement>("[data-scene]")
    .forEach((button, i) => {
      button.setAttribute("aria-selected", String(i === index));
      button.tabIndex = i === index ? 0 : -1;
    });
  document
    .querySelector("#scene-panel")
    ?.setAttribute("aria-labelledby", `scene-tab-${index}`);
  const fields = {
    headline: scene.headline,
    description: scene.description,
    benefit: scene.benefit,
  };
  Object.entries(fields).forEach(([name, value]) => {
    const el = document.querySelector(`[data-scene-${name}]`);
    if (el) el.textContent = value;
  });
  const tasks = document.querySelector("[data-scene-tasks]");
  if (tasks)
    tasks.innerHTML = scene.tasks
      .map((task, i) => `<div><span>0${i + 1}</span><p>${task}</p></div>`)
      .join("");
  const element = document.querySelector<HTMLElement>('[data-demo="scenario"]');
  if (element) setDemoScene(element, index);
}
if (!isDownload) {
  selectScene(0);
  document
    .querySelectorAll<HTMLButtonElement>("[data-scene]")
    .forEach((button) => {
      button.addEventListener("click", () =>
        selectScene(Number(button.dataset.scene)),
      );
      button.addEventListener("keydown", (event) => {
        if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key))
          return;
        event.preventDefault();
        selectScene(
          event.key === "Home"
            ? 0
            : event.key === "End"
              ? scenes.length - 1
              : (currentScene +
                  (event.key === "ArrowRight" ? 1 : -1) +
                  scenes.length) %
                scenes.length,
        );
        document
          .querySelector<HTMLButtonElement>(`[data-scene="${currentScene}"]`)
          ?.focus();
      });
    });
  const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
  const animate = () => {
    if (document.hidden || reducedMotion.matches) return;
    tick++;
    scenarioTick++;
    const phone = document.querySelector<HTMLElement>('[data-demo="scenario"]');
    if (phone) {
      const phoneStep = scenarioTick % 7;
      phone.dataset.step = String(Math.min(3, phoneStep));
      const phoneStatus = phone.querySelector("[data-demo-status]");
      if (phoneStatus)
        phoneStatus.textContent = phoneStep >= 3 ? "已完成" : "正在执行";
    }
    const step = tick % 6;
    const element = document.querySelector<HTMLElement>('[data-demo="hero"]')!;
    if (step === 0) {
      heroScene = (heroScene + 1) % scenes.length;
      setDemoScene(element, heroScene);
    }
    element.dataset.step = String(Math.min(3, step));
    const status = element.querySelector("[data-demo-status]");
    if (status) status.textContent = step >= 3 ? "已完成" : "正在执行";
    const word = document.querySelector("[data-rotating-word]");
    if (word && step === 0)
      word.textContent = ["电商运营", "内容创作", "日常工作"][heroScene]!;
  };
  if (reducedMotion.matches)
    document.querySelectorAll<HTMLElement>("[data-demo]").forEach((el) => {
      el.dataset.step = "3";
    });
  window.setInterval(animate, 2600);
  reducedMotion.addEventListener("change", () => {
    if (reducedMotion.matches)
      document.querySelectorAll<HTMLElement>("[data-demo]").forEach((el) => {
        el.dataset.step = "3";
      });
  });
}
function downloadURL(value: unknown): string | null {
  if (typeof value !== "string") return null;
  try {
    const url = new URL(value, location.origin);
    return url.origin === location.origin &&
      url.pathname.startsWith("/api/v1/client/releases/")
      ? url.href
      : null;
  } catch {
    return null;
  }
}
async function loadRelease() {
  if (!isDownload) return;
  const status = document.querySelector("[data-release-status]")!;
  const retry = document.querySelector<HTMLButtonElement>(".retry-release")!;
  retry.hidden = true;
  status.textContent = "正在获取最新版本…";
  try {
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 10000);
    let response: Response;
    try {
      response = await fetch("/api/v1/client/releases/latest", {
        cache: "no-store",
        signal: controller.signal,
      });
    } finally {
      clearTimeout(timeout);
    }
    if (response.status === 404) {
      status.textContent = "新版本即将发布";
      document
        .querySelectorAll("[data-package-meta]")
        .forEach((el) => (el.textContent = "安装包准备中"));
      return;
    }
    if (!response.ok) throw new Error("release unavailable");
    const envelope = await response.json();
    const release = envelope.data as Release;
    if (!release || typeof release.version !== "string" || !release.platforms)
      throw new Error("invalid release");
    status.textContent = `最新版本 v${release.version}`;
    document
      .querySelectorAll<HTMLAnchorElement>("[data-download-platform]")
      .forEach((link) => {
        const platform = link.dataset.downloadPlatform as Platform;
        const artifact = release.platforms[platform];
        const url = artifact && downloadURL(artifact.download_url);
        const meta = document.querySelector(
          `[data-package-meta="${platform}"]`,
        )!;
        if (!artifact || !url) {
          meta.textContent = "此平台安装包暂未发布";
          return;
        }
        link.href = url;
        link.classList.remove("is-disabled");
        link.removeAttribute("aria-disabled");
        link.removeAttribute("tabindex");
        meta.textContent = `v${release.version} · ${(artifact.size_bytes / 1024 / 1024).toFixed(1)} MB`;
      });
  } catch {
    status.textContent = "暂时无法获取版本，请稍后重试";
    document
      .querySelectorAll("[data-package-meta]")
      .forEach((el) => (el.textContent = "暂不可用"));
    retry.hidden = false;
  }
}
document
  .querySelector(".retry-release")
  ?.addEventListener("click", () => void loadRelease());
void loadRelease();
