import Image from "next/image";
import Link from "next/link";
import dynamic from "next/dynamic";
import logo from "../../public/logo.png";
import appShot from "../../public/app.png";
import statusesShot from "../../public/statuses.png";
import browserShot from "../../public/browser.png";
import regenerateShot from "../../public/regenerate.png";
import styles from "./page.module.css";

const ThemeToggle = dynamic(() => import("./components/ThemeToggle"), {});
const CopyButton = dynamic(() => import("./components/CopyButton"), {});

const downloadUrl = "https://github.com/thirschel/Hangar/releases/latest";
const windowsInstall = `git clone https://github.com/thirschel/Hangar.git
cd Hangar
build.bat`;
const desktopBuild = "cd desktop && npm run dist";

const homebrewInstall = "brew install claude-squad";
const shellInstall = "curl -fsSL https://raw.githubusercontent.com/smtg-ai/claude-squad/main/install.sh | bash";

const features = [
  {
    title: "Runs natively on Windows",
    body: "No WSL, no tmux. A background session host owns a real Windows console per agent; the cs TUI talks to it over a named pipe and renders via a VT emulator, so sessions survive TUI restarts.",
  },
  {
    title: "Your tools, PATH & auth",
    body: "Each agent runs in a real Windows console (ConPTY), so it inherits the terminal you already use — the tools and apps on your PATH, your internal tooling, and, most importantly, your existing auth. If your CLI works in your terminal, it works in Hangar.",
  },
  {
    title: "Supervise multiple agents at once",
    body: "Manage Claude Code, Codex, Gemini, GitHub Copilot CLI, and Aider side by side in one terminal dashboard.",
  },
  {
    title: "Isolated git worktrees",
    body: "Every session works on its own branch in its own worktree, so tasks never collide.",
  },
  {
    title: "Review before you ship",
    body: "Inspect each session's diff, then commit & push or checkout & pause — on your terms.",
  },
  {
    title: "Background + AutoYes",
    body: "Agents keep working and auto-accept prompts even while the TUI is closed; AutoYes pauses automatically while you're attached.",
  },
];

export default function Home() {
  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <Link className={styles.brand} href="/" aria-label="Hangar home">
          <span className={`${styles.logoChip} ${styles.headerLogoChip}`}>
            <Image src={logo} alt="Hangar logo" className={styles.headerLogo} priority />
          </span>
          <span className={styles.wordmark}>Hangar</span>
        </Link>
        <div className={styles.headerActions}>
          <a
            className={styles.headerButton}
            href="https://github.com/thirschel/Hangar"
            target="_blank"
            rel="noopener noreferrer"
          >
            GitHub
          </a>
          <a
            href="https://github.com/thirschel/Hangar#readme"
            target="_blank"
            rel="noopener noreferrer"
            className={styles.headerButton}
          >
            Docs
          </a>
          <ThemeToggle />
        </div>
      </header>

      <main className={styles.main}>
        <section className={styles.hero} aria-labelledby="hero-title">
          <span className={`${styles.logoChip} ${styles.heroLogoChip}`}>
            <Image src={logo} alt="Hangar logo" className={styles.heroLogo} priority />
          </span>
          <p className={styles.eyebrow}>A hangar for all your copilots.</p>
          <h1 id="hero-title">Hangar</h1>
          <p className={styles.positioning}>
            A lightweight harness around your favorite CLI coding agent — run several in parallel on native Windows, each in its own isolated git worktree, and review their work before it ships.
          </p>
          <div className={styles.ctaGroup} aria-label="Primary actions">
            <a
              className={`${styles.ctaButton} ${styles.ctaButtonPrimary}`}
              href={downloadUrl}
              target="_blank"
              rel="noopener noreferrer"
            >
              Download for Windows
            </a>
            <a className={`${styles.ctaButton} ${styles.ctaButtonSecondary}`} href="#install">
              Build from source
            </a>
          </div>
        </section>

        <div className={styles.demoVideo}>
          <Image
            src={appShot}
            alt="Hangar Desktop — workspaces, the active agent session, and a live diff of its changes"
            className={styles.heroShot}
            priority
          />
        </div>

        <section className={styles.features} id="features" aria-labelledby="features-title">
          <div className={styles.sectionIntro}>
            <p className={styles.sectionKicker}>Features</p>
            <h2 id="features-title">Built for busy agent fleets</h2>
          </div>
          <div className={styles.featureGrid}>
            {features.map((feature, index) => (
              <article className={styles.featureCard} key={feature.title}>
                <span className={styles.featureNumber}>{String(index + 1).padStart(2, "0")}</span>
                <h3>{feature.title}</h3>
                <p>{feature.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className={styles.showcase} id="screenshots" aria-labelledby="showcase-title">
          <div className={styles.sectionIntro}>
            <p className={styles.sectionKicker}>Screenshots</p>
            <h2 id="showcase-title">A closer look</h2>
          </div>
          <div className={styles.showcaseGrid}>
            <figure className={styles.shot}>
              <div className={styles.shotFrame}>
                <Image
                  src={statusesShot}
                  alt="Status filtering and grouping in the Hangar sidebar"
                  className={styles.shotImage}
                />
              </div>
              <figcaption className={styles.shotCaption}>
                Status filtering &amp; grouping — filter workspaces by Waiting / Busy / Idle / Exited and group them
                by repo, with live per-status counts.
              </figcaption>
            </figure>
            <figure className={styles.shot}>
              <div className={styles.shotFrame}>
                <Image src={browserShot} alt="Copilot Session Browser" className={styles.shotImage} />
              </div>
              <figcaption className={styles.shotCaption}>
                Copilot Session Browser — search and resume your local GitHub Copilot CLI sessions in a fresh
                isolated worktree.
              </figcaption>
            </figure>
            <figure className={styles.shot}>
              <div className={styles.shotFrame}>
                <Image
                  src={regenerateShot}
                  alt="Regenerate an agent with an optional handoff document"
                  className={styles.shotImage}
                />
              </div>
              <figcaption className={styles.shotCaption}>
                Regenerate with handoff — restart an agent in place, optionally writing a HANDOFF.md so the fresh
                agent keeps its context.
              </figcaption>
            </figure>
          </div>
        </section>

        <section className={styles.installation} id="install" aria-labelledby="install-title">
          <div className={styles.sectionIntro}>
            <p className={styles.sectionKicker}>Start</p>
            <h2 id="install-title">Install Hangar</h2>
          </div>

          <article className={`${styles.installCard} ${styles.downloadCard}`}>
            <div>
              <p className={styles.platform}>Windows desktop app</p>
              <h3>Download the app</h3>
              <p>
                Get the <code>Hangar-Setup-&lt;version&gt;.exe</code> NSIS installer from Releases. It installs
                Hangar, supports auto-updates, and is the fastest way to start supervising agents from the desktop.
              </p>
            </div>
            <div className={styles.downloadActions}>
              <a
                className={`${styles.ctaButton} ${styles.ctaButtonPrimary}`}
                href={downloadUrl}
                target="_blank"
                rel="noopener noreferrer"
              >
                Download for Windows
              </a>
              <p className={styles.downloadNote}>
                The installer is currently unsigned, so Windows SmartScreen may warn. No release yet? Build from
                source below.
              </p>
            </div>
          </article>

          <div className={styles.installSubhead}>
            <p className={styles.sectionKicker}>Under the hood</p>
            <h3>Build the daemon/CLI</h3>
            <p>
              Hangar is a thin Electron client over the Go <code>cs</code> core-daemon. You can build the daemon
              and TUI from source, or package the desktop app locally.
            </p>
          </div>

          <div className={styles.installGrid}>
            <article className={styles.installCard}>
              <div>
                <p className={styles.platform}>Windows source build</p>
                <h3>Build the daemon and TUI</h3>
                <p>
                  Requires Go 1.25+, git, gh, and your agent executable, such as GitHub Copilot CLI, resolvable via <code>where copilot</code>.
                </p>
              </div>
              <div className={styles.codeBlockWrapper}>
                <pre className={styles.codeBlock}>
                  <code>{windowsInstall}</code>
                </pre>
                <CopyButton textToCopy={windowsInstall} />
              </div>
              <p className={styles.note}>
                Then put <code>cs.exe</code> on PATH and run <code>cs</code> inside a git repo.
              </p>
              <div className={styles.codeBlockWrapper}>
                <pre className={styles.codeBlock}>
                  <code>{desktopBuild}</code>
                </pre>
                <CopyButton textToCopy={desktopBuild} />
              </div>
              <p className={styles.note}>
                Or package the Electron desktop app locally from the repository root.
              </p>
            </article>

            <article className={styles.installCard}>
              <div>
                <p className={styles.platform}>Unix/macOS</p>
                <h3>Install upstream claude-squad</h3>
                <p>
                  Unix installs use the upstream binary. Prerequisites: gh and tmux on Unix/macOS/WSL.
                </p>
              </div>
              <div className={styles.codeBlockWrapper}>
                <pre className={styles.codeBlock}>
                  <code>{homebrewInstall}</code>
                </pre>
                <CopyButton textToCopy={homebrewInstall} />
              </div>
              <div className={styles.codeBlockWrapper}>
                <pre className={styles.codeBlock}>
                  <code>{shellInstall}</code>
                </pre>
                <CopyButton textToCopy={shellInstall} />
              </div>
            </article>
          </div>
        </section>
      </main>

      <footer className={styles.footer}>
        <p>
          Licensed under{" "}
          <a href="https://github.com/thirschel/Hangar/blob/main/LICENSE.md" target="_blank" rel="noopener noreferrer">
            GNU AGPL v3.0
          </a>
          .
        </p>
        <p>
          Hangar is a fork of{" "}
          <a href="https://github.com/smtg-ai/claude-squad" target="_blank" rel="noopener noreferrer">
            smtg-ai/claude-squad
          </a>
          .
        </p>
      </footer>
    </div>
  );
}
