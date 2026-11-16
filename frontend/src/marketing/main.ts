import "./site.css";

type PageId = "home" | "product" | "scenarios" | "cloud" | "download";
type Theme = "light" | "dark";
type Platform = "windows-x64" | "macos-arm64" | "macos-arm64-dmg";
type Artifact = { filename: string; size_bytes: number; sha256: string; download_url: string };
type Release = { version: string; notes?: string; published_at?: string; platforms: Partial<Record<Platform, Artifact>> };

const themeSkin: Record<Theme, string> = { light: "daylight", dark: "midnight" };
const currentTheme = (): Theme => document.documentElement.dataset.theme === "dark" ? "dark" : "light";

const routes: Record<string, PageId> = {
  "/": "home",
  "/product": "product",
  "/scenarios": "scenarios",
  "/cloud": "cloud",
  "/download": "download",
};
const page = routes[location.pathname.replace(/\/$/, "") || "/"] ?? "home";
const navItems: Array<{ href: string; id: PageId; label: string }> = [
  { href: "/product", id: "product", label: "产品能力" },
  { href: "/scenarios", id: "scenarios", label: "使用场景" },
  { href: "/cloud", id: "cloud", label: "云端与手机" },
  { href: "/download", id: "download", label: "下载客户端" },
];

function header(): string {
  return `<header class="site-header">
    <a class="brand" href="/" aria-label="DoneBot 首页"><img class="brand-mark" src="/donebot-logo.png" alt="" /><span>DoneBot</span></a>
    <nav class="site-nav" aria-label="主导航">${navItems.map((item) => `<a class="${page === item.id ? "is-current" : ""}" href="${item.href}">${item.label}</a>`).join("")}</nav>
    <div class="header-actions"><div class="theme-toggle" role="group" aria-label="选择显示主题"><button type="button" data-theme-choice="light" aria-label="浅色模式" title="浅色模式" aria-pressed="${currentTheme() === "light"}"><img class="theme-icon" src="/marketing-icons/sun.svg" alt="" /></button><button type="button" data-theme-choice="dark" aria-label="深色模式" title="深色模式" aria-pressed="${currentTheme() === "dark"}"><img class="theme-icon" src="/marketing-icons/moon.svg" alt="" /></button></div></div>
  </header>`;
}

function footer(): string {
  return `<footer class="site-footer">
    <a class="brand" href="/"><img class="brand-mark" src="/donebot-logo.png" alt="" /><span>DoneBot</span></a>
    <span>一个机器人，接住从想法到交付的工作。</span>
    <a href="/download">获取最新版 <span aria-hidden="true">↗</span></a>
  </footer>`;
}

function clientFrame(scene: "store" | "tiktok" | "studio", extraClass = "", phone = false): string {
  const query = new URLSearchParams({ scene, skin: themeSkin[currentTheme()] });
  if (phone) query.set("layout", "phone");
  const src = `/client-demo/client-demo.html?${query.toString().replace(/&/g, "&amp;")}`;
  return `<div class="client-stage ${extraClass}">
    <iframe title="DoneBot 实际客户端动态演示" src="${src}" loading="eager" allow="clipboard-read; clipboard-write"></iframe>
    <div class="client-stage-caption"><span class="live-dot"></span><span>DoneBot 客户端 · 实时演示</span><span class="caption-scene">${scene === "store" ? "独立站协作" : scene === "tiktok" ? "短视频投放" : "内容研究"}</span></div>
  </div>`;
}

function home(): string {
  return `<main class="page page-home">
    <section class="home-hero section-dark">
      <div class="hero-copy reveal">
        <p class="eyebrow"><span class="eyebrow-rule"></span> 7×24 云端机器人团队</p>
        <h1>机器人团队<br /><span>持续工作。</span></h1>
        <p class="hero-lede">你下班后，DoneBot 仍在云端搜集信息、整理文件、制作内容并推进任务。手机随时查看和补充，早上团队直接接手结果。</p>
        <div class="hero-actions"><a class="button button-red" href="/download">下载 DoneBot <span aria-hidden="true">↗</span></a><a class="text-link" href="/product">看看它能做什么 <span aria-hidden="true">→</span></a></div>
        <div class="hero-latest">当前最新版 <strong data-latest-version>正在获取</strong></div>
      </div>
      <div class="hero-live reveal reveal-late">
        <div class="demo-label"><span>01 / 一条任务，完整交付</span><span class="demo-label-live"><i></i> LIVE</span></div>
        ${clientFrame("store", "client-stage-home")}
        <div class="hero-flow" aria-label="全天候协作流程"><span>你交代任务</span><b></b><span>云端整夜推进</span><b></b><span>手机与团队接力</span></div>
      </div>
      <a class="scroll-cue" href="#workflows"><span></span> 向下探索</a>
    </section>
    <section class="workflow-intro section-light" id="workflows">
      <div class="section-kicker reveal">不是只会回答</div>
      <div class="workflow-head reveal"><h2>从一句需求，<br />到一份能用的结果。</h2><p>DoneBot 会理解上下文、调用需要的应用、把中间产物整理好，再把下一步接上。</p></div>
      <div class="workflow-rail" data-reveal-group>
        <article class="workflow-step is-active"><span class="step-index">01</span><div><h3>说清目标</h3><p>用自然语言交代要解决的问题、背景和交付方式。</p></div></article>
        <article class="workflow-step"><span class="step-index">02</span><div><h3>机器人展开工作</h3><p>查资料、读文档、连接应用、生成内容，按任务推进。</p></div></article>
        <article class="workflow-step"><span class="step-index">03</span><div><h3>结果交回团队</h3><p>文稿、素材、视频草稿与后续任务保持在同一条工作线上。</p></div></article>
      </div>
    </section>
    <section class="media-strip section-red">
      <div class="media-strip-head"><p class="eyebrow"><span class="eyebrow-rule"></span> 一种能力，多种产出</p><h2>文档、图片、视频、音乐，<br />都能在任务里完成。</h2></div>
      <div class="media-words" aria-label="内容类型"><span>文档</span><span>图片</span><span>视频</span><span>音乐</span></div>
      <a class="light-link" href="/product">探索产品能力 <span aria-hidden="true">→</span></a>
    </section>
    <section class="scenes-preview section-light">
      <div class="section-kicker reveal">放进真实业务里</div>
      <div class="scenes-head reveal"><h2>同一个机器人，<br />接住不同团队的工作。</h2><a class="text-link" href="/scenarios">查看场景 <span aria-hidden="true">→</span></a></div>
      <div class="scene-lines" data-reveal-group>
        <a href="/scenarios#store"><span>01</span><strong>独立站团队</strong><span>调研 → 页面文案 → 素材任务</span><span class="scene-arrow">↗</span></a>
        <a href="/scenarios#tiktok"><span>02</span><strong>TikTok Shop</strong><span>卖点 → 脚本分镜 → 广告视频</span><span class="scene-arrow">↗</span></a>
        <a href="/scenarios#studio"><span>03</span><strong>自媒体工作室</strong><span>搜集信息 → 选题素材 → 剪辑草稿</span><span class="scene-arrow">↗</span></a>
      </div>
    </section>
    <section class="apps-band section-dark">
      <div class="apps-count reveal"><strong>1,000<span>+</span></strong><span>可连接的应用</span></div>
      <div class="apps-copy reveal"><p class="eyebrow"><span class="eyebrow-rule"></span> 让工作留在原来的地方</p><h2>一个机器人，<br />连接团队已经在用的工具。</h2><p>邮件、文档、表格、商店、社交平台与自动化服务，按任务接入，让信息和动作连起来。</p><a class="text-link text-link-light" href="/product">了解应用连接 <span aria-hidden="true">→</span></a></div>
      <div class="app-flow" aria-hidden="true"><span>SHOPIFY</span><i></i><span>NOTION</span><i></i><span>GOOGLE DRIVE</span><i></i><span>TIKTOK</span><i></i><span>SLACK</span></div>
    </section>
    <section class="last-cta section-light"><p class="eyebrow"><span class="eyebrow-rule"></span> 把时间留给更重要的工作</p><h2>交代一次，<br />让 DoneBot 接着做。</h2><a class="button button-dark" href="/download">下载客户端 <span aria-hidden="true">↗</span></a></section>
  </main>`;
}

function product(): string {
  return `<main class="page page-product">
    <section class="page-hero section-dark product-hero"><div class="page-hero-copy reveal"><p class="eyebrow"><span class="eyebrow-rule"></span> 产品能力</p><h1>把多种工作，<br /><span>交给同一个机器人。</span></h1><p>DoneBot 不止生成答案。它能在同一段任务里搜集信息、处理文件、创作多媒体，再连接你正在用的应用。</p><a class="button button-red" href="/download">开始使用 <span aria-hidden="true">↗</span></a></div><div class="product-live reveal reveal-late">${clientFrame("tiktok", "client-stage-product")}</div></section>
    <section class="capabilities section-light"><div class="section-kicker reveal">从信息到成品</div><h2 class="capabilities-title reveal">工作需要什么，<br />它就接着做什么。</h2>
      <div class="capability-list" data-reveal-group>
        <article><span class="capability-no">01</span><div><h3>文档与研究</h3><p>搜索资料、比对信息、汇总报告，把来源和结论放在一起。</p></div><span class="capability-result">找信息 → 有依据的判断</span></article>
        <article><span class="capability-no">02</span><div><h3>图片与设计素材</h3><p>围绕产品卖点生成图像方向和素材，服务页面、广告与内容创作。</p></div><span class="capability-result">有想法 → 可用素材</span></article>
        <article><span class="capability-no">03</span><div><h3>视频与剪辑</h3><p>从脚本、分镜到视频素材与剪辑草稿，让创意更快进入制作。</p></div><span class="capability-result">一个主题 → 多条视频</span></article>
        <article><span class="capability-no">04</span><div><h3>音乐与声音</h3><p>为内容制作合适的音乐，让视频从画面到声音都准备好。</p></div><span class="capability-result">画面 → 完整表达</span></article>
      </div>
    </section>
    <section class="connected-work section-red"><div class="connected-work-copy reveal"><p class="eyebrow"><span class="eyebrow-rule"></span> 应用扩展是机器人的能力</p><h2>连接 1,000+ 个应用，<br />让任务跨过工具边界。</h2><p>当资料在文档、订单在商店、讨论在团队工具里，DoneBot 可以按你的授权调用应用，把动作接起来。</p><a class="light-link" href="/scenarios">看团队怎么接力 <span aria-hidden="true">→</span></a></div><div class="connector-flow reveal reveal-late"><div class="connector-node">任务<br /><strong>找出高转化卖点</strong></div><div class="connector-path"><span>读取评价</span><i></i><span>写页面</span><i></i><span>同步素材任务</span></div><div class="connector-node connector-node-end">交付<br /><strong>结果回到团队</strong></div></div></section>
    <section class="product-principle section-light"><div class="section-kicker reveal">一直在同一条线上</div><div class="principle-columns"><h2 class="reveal">上下文不断，<br />工作才不会重来。</h2><div class="principle-copy reveal"><p>机器人记得当前任务的目标和进度。你可以继续追问、补充文件、调整方向，或让团队成员从结果接手。</p><p>从本地客户端到云端工作空间，DoneBot 围绕同一套机器人体验延展。</p><a class="text-link" href="/cloud">了解云端与手机 <span aria-hidden="true">→</span></a></div></div></section>
  </main>`;
}

function scenarios(): string {
  return `<main class="page page-scenarios">
    <section class="page-hero section-dark scenarios-hero"><div class="page-hero-copy reveal"><p class="eyebrow"><span class="eyebrow-rule"></span> 使用场景</p><h1>一条工作链路，<br /><span>让团队少切换。</span></h1><p>从研究到内容，从商品卖点到广告素材，DoneBot 把多角色之间容易断开的工作连起来。</p></div><div class="scenario-live reveal reveal-late">${clientFrame("store", "client-stage-scenarios")}</div></section>
    <section class="scenario-tabs section-light"><div class="scene-selector" role="tablist" aria-label="选择团队场景">
      <button class="scene-tab is-active" role="tab" aria-selected="true" data-scene="store"><span>01</span>独立站团队</button><button class="scene-tab" role="tab" aria-selected="false" data-scene="tiktok"><span>02</span>TikTok Shop</button><button class="scene-tab" role="tab" aria-selected="false" data-scene="studio"><span>03</span>自媒体工作室</button>
    </div><div class="scene-detail" data-scene-detail>
      <div class="scene-detail-copy"><p class="eyebrow"><span class="eyebrow-rule"></span><span data-scene-eyebrow>商品研究到上架</span></p><h2 data-scene-title>让商品页和素材，<br />从同一份研究开始。</h2><p data-scene-copy>先归纳评论里的真实购买理由，再完成详情页结构、广告卖点和拍摄任务。运营、设计和投放不必反复找同一份资料。</p><div class="scene-outcome"><span>带来的变化</span><strong data-scene-outcome>研究、文案、素材一次交接，减少等待和重复沟通。</strong></div></div><div class="scene-steps" data-scene-steps><div class="scene-step is-active"><span>01</span><div><strong>读评论与竞品</strong><small>快速看见购买动机和顾虑</small></div></div><div class="scene-step"><span>02</span><div><strong>生成页面与广告内容</strong><small>把卖点变成可发布的表达</small></div></div><div class="scene-step"><span>03</span><div><strong>同步团队下一步</strong><small>让素材工作直接开始</small></div></div></div>
    </div></section>
    <section class="scenario-video section-red"><div class="scenario-video-copy reveal"><p class="eyebrow"><span class="eyebrow-rule"></span> 视频广告从目标出发</p><h2>不是只写一段脚本，<br />而是把广告做成投放准备。</h2><p>围绕产品卖点生成 Hook、分镜、字幕和视频素材，让团队可以更快制作不同角度的创意。</p></div><div class="ad-angles" data-reveal-group><span>痛点开场</span><span>对比演示</span><span>真实场景</span><span>优惠行动</span></div></section>
    <section class="studio-work section-light"><div class="studio-work-copy reveal"><p class="eyebrow"><span class="eyebrow-rule"></span> 内容团队的素材流水线</p><h2>从信息过载，<br />到下一条值得发的内容。</h2><p>机器人跟踪变化，筛出值得做的题材，整理可靠来源与素材，再协助制作短视频草稿。</p></div><div class="studio-steps" data-reveal-group><div><span>收集</span><strong>行业变化与受众反馈</strong></div><div><span>筛选</span><strong>有角度、有来源的选题</strong></div><div><span>制作</span><strong>文案、画面、字幕、配乐</strong></div><div><span>交付</span><strong>可继续编辑的视频草稿</strong></div></div></section>
    <section class="last-cta section-dark"><p class="eyebrow"><span class="eyebrow-rule"></span> 让每个人少做重复劳动</p><h2>下一条任务，<br />交给 DoneBot 接手。</h2><a class="button button-red" href="/download">下载客户端 <span aria-hidden="true">↗</span></a></section>
  </main>`;
}

function cloud(): string {
  return `<main class="page page-cloud">
    <section class="cloud-hero section-dark"><div class="cloud-copy reveal"><p class="eyebrow"><span class="eyebrow-rule"></span> 7×24 云端与手机接力</p><h1>电脑合上了，<br /><span>工作仍在继续。</span></h1><p>让任务留在云端持续推进。你从手机查看进度、补充方向或确认关键动作；需要团队接手时，研究、文件和创作结果已经整理好。</p><a class="button button-red" href="/download">下载客户端 <span aria-hidden="true">↗</span></a></div><div class="phone-preview reveal reveal-late"><div class="phone-camera"></div>${clientFrame("studio", "client-stage-phone", true)}</div><div class="cloud-caption">云端持续运行 · 手机随时接力 · 团队拿到结果</div></section>
    <section class="continuity section-light"><div class="section-kicker reveal">不必守在电脑前</div><div class="continuity-head reveal"><h2>让机器人团队<br />接过跨时段工作。</h2><p>云端持续推进，手机成为轻量的任务入口。机器人把需要你决定的部分送到面前，也把结果带回来。</p></div><div class="continuity-list" data-reveal-group><article><span>01</span><div><h3>云端 7×24 持续工作</h3><p>研究、整理、创作任务留在云端，不因你离开电脑而中断。</p></div><strong>全天候推进</strong></article><article><span>02</span><div><h3>手机接住关键时刻</h3><p>查看进度、补充上下文、确认需要你参与的步骤。</p></div><strong>随时接力</strong></article><article><span>03</span><div><h3>团队按结果继续</h3><p>文档、素材和任务上下文都在同一条工作线上，减少早晨重新交接。</p></div><strong>醒来即可接手</strong></article></div></section>
    <section class="mobile-scenes section-red"><p class="eyebrow"><span class="eyebrow-rule"></span> 适合真实工作节奏</p><h2>早上交代，<br />路上跟进，<br />回到工位拿结果。</h2><div class="mobile-timeline"><span>电脑</span><i></i><span>云端</span><i></i><span>手机</span><i></i><span>团队</span></div></section>
    <section class="last-cta section-light"><p class="eyebrow"><span class="eyebrow-rule"></span> 从桌面开始，也不止于桌面</p><h2>你的机器人，<br />跟着工作一起移动。</h2><a class="button button-dark" href="/download">获取 DoneBot <span aria-hidden="true">↗</span></a></section>
  </main>`;
}

function download(): string {
  return `<main class="page page-download">
    <section class="download-hero section-dark"><p class="eyebrow reveal"><span class="eyebrow-rule"></span> 下载 DoneBot</p><h1 class="reveal">把机器人，<br /><span>带进你的工作。</span></h1><p class="download-intro reveal">选择适合你设备的最新版客户端。版本号会从 DoneBot 发布服务实时读取。</p><div class="release-status" data-release-status role="status">正在获取最新版本…</div>
      <div class="download-options" data-reveal-group>
        <article class="download-option"><div class="platform-icon platform-windows" aria-hidden="true"><img src="/marketing-icons/windows.svg" alt="" /></div><div class="platform-content"><span class="platform-label">WINDOWS</span><h2>Windows x64</h2><p data-artifact-windows>正在检查安装包</p></div><a class="download-link is-disabled" data-download-platform="windows-x64" href="#" aria-disabled="true">下载 <span aria-hidden="true">↓</span></a></article>
        <article class="download-option"><div class="platform-icon platform-mac" aria-hidden="true"><img src="/marketing-icons/apple.svg" alt="" /></div><div class="platform-content"><span class="platform-label">MACOS</span><h2>Apple 芯片</h2><p data-artifact-mac>正在检查 DMG 安装镜像</p></div><a class="download-link is-disabled" data-download-platform="macos-arm64-dmg" href="#" aria-disabled="true">下载 DMG <span aria-hidden="true">↓</span></a></article>
      </div>
      <div class="release-note"><span>最新版</span><strong data-latest-version>正在获取</strong><span data-release-date></span></div>
    </section>
    <section class="updates section-light"><div class="updates-copy"><p class="eyebrow"><span class="eyebrow-rule"></span> 保持更新</p><h2>新版本，<br />机器人会告诉你。</h2><p>DoneBot 客户端会检查当前系统对应的最新版本，下载并安装适合的更新。</p></div><div class="update-flow"><div><span>客户端</span><strong>检查当前版本</strong></div><i></i><div><span>DoneBot</span><strong>返回最新版本</strong></div><i></i><div><span>完成</span><strong>获取对应安装包</strong></div></div></section>
  </main>`;
}

const content = ({ home, product, scenarios, cloud, download } satisfies Record<PageId, () => string>)[page]();
document.getElementById("donebot-site")!.innerHTML = `${header()}${content}${footer()}`;

function setSiteTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  try {
    localStorage.setItem("donebot-site-theme", theme);
  } catch {
  }
  const themeColor = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
  if (themeColor) themeColor.content = theme === "dark" ? "#000000" : "#ffffff";
  document.querySelectorAll<HTMLButtonElement>("[data-theme-choice]").forEach((button) => {
    button.setAttribute("aria-pressed", String(button.dataset.themeChoice === theme));
  });
  document.querySelectorAll<HTMLIFrameElement>(".client-stage iframe").forEach((frame) => {
    const url = new URL(frame.src, location.href);
    if (url.searchParams.get("skin") === themeSkin[theme]) return;
    url.searchParams.set("skin", themeSkin[theme]);
    frame.src = `${url.pathname}${url.search}${url.hash}`;
  });
}

document.querySelectorAll<HTMLButtonElement>("[data-theme-choice]").forEach((button) => {
  button.addEventListener("click", () => setSiteTheme(button.dataset.themeChoice === "dark" ? "dark" : "light"));
});
document.body.dataset.page = page;

const sceneContent: Record<string, { eyebrow: string; title: string; copy: string; outcome: string; steps: string[] }> = {
  store: {
    eyebrow: "商品研究到上架",
    title: "让商品页和素材，<br />从同一份研究开始。",
    copy: "先归纳评论里的真实购买理由，再完成详情页结构、广告卖点和拍摄任务。运营、设计和投放不必反复找同一份资料。",
    outcome: "研究、文案、素材一次交接，减少等待和重复沟通。",
    steps: ["读评论与竞品|快速看见购买动机和顾虑", "生成页面与广告内容|把卖点变成可发布的表达", "同步团队下一步|让素材工作直接开始"],
  },
  tiktok: {
    eyebrow: "TikTok Shop 广告视频",
    title: "从一个商品，<br />做出多角度投放素材。",
    copy: "围绕便携、续航和清洗生成不同开场，自动组织脚本、分镜、字幕与音乐方向，让团队更快进入剪辑和测试。",
    outcome: "一个产品卖点，变成多条可以测试的创意角度。",
    steps: ["提炼商品卖点|从商品信息和评价找证据", "生成脚本与分镜|围绕不同人群设计 Hook", "制作视频素材|整理画面、字幕和音乐方向"],
  },
  studio: {
    eyebrow: "自媒体研究与创作",
    title: "从热点线索，<br />接到能发布的视频草稿。",
    copy: "持续搜集可靠信息，筛选有差异的选题，整理原始素材，再把文稿、画面、字幕和配乐接进剪辑流程。",
    outcome: "减少找题和找素材的时间，把精力留给观点和表达。",
    steps: ["跟踪信息变化|收集更新、反馈和可靠来源", "筛选内容题材|按热度、受众和差异化排序", "生成剪辑草稿|文案、素材、字幕与配乐衔接"],
  },
};

const demoFrame = document.querySelector<HTMLIFrameElement>(".client-stage-scenarios iframe");
const sceneTabs = [...document.querySelectorAll<HTMLButtonElement>(".scene-tab")];
function selectScene(scene: string): void {
  const data = sceneContent[scene];
  if (!data) return;
  sceneTabs.forEach((tab) => {
    const active = tab.dataset.scene === scene;
    tab.classList.toggle("is-active", active);
    tab.setAttribute("aria-selected", String(active));
  });
  const eyebrow = document.querySelector<HTMLElement>("[data-scene-eyebrow]");
  const title = document.querySelector<HTMLElement>("[data-scene-title]");
  const copy = document.querySelector<HTMLElement>("[data-scene-copy]");
  const outcome = document.querySelector<HTMLElement>("[data-scene-outcome]");
  if (eyebrow) eyebrow.textContent = data.eyebrow;
  if (title) title.innerHTML = data.title;
  if (copy) copy.textContent = data.copy;
  if (outcome) outcome.textContent = data.outcome;
  const steps = document.querySelector<HTMLElement>("[data-scene-steps]");
  if (steps) steps.innerHTML = data.steps.map((item, index) => {
    const [heading, description] = item.split("|");
    return `<div class="scene-step ${index === 0 ? "is-active" : ""}"><span>0${index + 1}</span><div><strong>${heading}</strong><small>${description}</small></div></div>`;
  }).join("");
  if (demoFrame) demoFrame.src = `/client-demo/client-demo.html?scene=${scene}&skin=${themeSkin[currentTheme()]}`;
}
sceneTabs.forEach((tab) => tab.addEventListener("click", () => {
  const scene = tab.dataset.scene ?? "store";
  selectScene(scene);
  history.replaceState(null, "", `#${scene}`);
}));
const initialScene = location.hash.slice(1);
selectScene(sceneContent[initialScene] ? initialScene : "store");

const revealObserver = new IntersectionObserver((entries) => {
  entries.forEach((entry) => {
    if (entry.isIntersecting) {
      entry.target.classList.add("is-visible");
      revealObserver.unobserve(entry.target);
    }
  });
}, { threshold: 0.12 });
document.querySelectorAll(".reveal, [data-reveal-group] > *").forEach((node) => revealObserver.observe(node));

const workflowObserver = new IntersectionObserver((entries) => {
  entries.forEach((entry) => {
    if (!entry.isIntersecting) return;
    const steps = [...document.querySelectorAll<HTMLElement>(".workflow-step")];
    let active = 0;
    window.setInterval(() => {
      steps.forEach((step, index) => step.classList.toggle("is-active", index === active));
      active = (active + 1) % steps.length;
    }, 2_100);
    workflowObserver.disconnect();
  });
}, { threshold: 0.3 });
const workflowRail = document.querySelector(".workflow-rail");
if (workflowRail) workflowObserver.observe(workflowRail);

function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "";
  return `${(bytes / (1024 * 1024)).toFixed(0)} MB`;
}

async function loadLatestRelease(): Promise<void> {
  const status = document.querySelector<HTMLElement>("[data-release-status]");
  try {
    const response = await fetch("/api/v1/client/releases/latest", { cache: "no-store", credentials: "omit" });
    if (response.status === 404) {
      document.querySelectorAll<HTMLElement>("[data-latest-version]").forEach((element) => { element.textContent = "暂未发布"; });
      document.querySelectorAll<HTMLElement>("[data-artifact-windows], [data-artifact-mac]").forEach((element) => { element.textContent = "暂未发布"; });
      if (status) status.textContent = "暂无已发布版本";
      return;
    }
    if (!response.ok) throw new Error("release service unavailable");
    const envelope = await response.json() as { data?: Release } & Release;
    const release = envelope.data ?? envelope;
    const version = release.version || "";
    document.querySelectorAll<HTMLElement>("[data-latest-version]").forEach((element) => {
      element.textContent = version ? `v${version}` : "暂未发布";
    });
    const date = document.querySelector<HTMLElement>("[data-release-date]");
    if (date) date.textContent = release.published_at ? new Date(release.published_at).toLocaleDateString("zh-CN") : "";
    const entries: Array<[Platform, string, string]> = [
      ["windows-x64", "[data-artifact-windows]", ""],
      ["macos-arm64-dmg", "[data-artifact-mac]", "DMG 安装镜像 · "],
    ];
    entries.forEach(([platform, selector, label]) => {
      const artifact = release.platforms?.[platform];
      const description = document.querySelector<HTMLElement>(selector);
      const link = document.querySelector<HTMLAnchorElement>(`[data-download-platform="${platform}"]`);
      if (description) description.textContent = artifact ? `${label}${version ? `v${version} · ` : ""}${formatSize(artifact.size_bytes)}` : "该平台安装包尚未发布";
      if (link && artifact?.download_url) {
        link.href = artifact.download_url;
        link.classList.remove("is-disabled");
        link.removeAttribute("aria-disabled");
      }
    });
    if (status) status.textContent = version ? `最新版 v${version} 已就绪` : "暂无已发布版本";
  } catch {
    if (status) status.textContent = "暂时无法获取版本信息，请稍后重试。";
    document.querySelectorAll<HTMLElement>("[data-latest-version]").forEach((element) => { element.textContent = "暂不可用"; });
    document.querySelectorAll<HTMLElement>("[data-artifact-windows], [data-artifact-mac]").forEach((element) => { element.textContent = "暂时无法连接发布服务"; });
  }
}

if (page === "home" || page === "download") void loadLatestRelease();
