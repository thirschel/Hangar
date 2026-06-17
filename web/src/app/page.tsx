import Image from "next/image";
import Link from "next/link";
import dynamic from "next/dynamic";
import logo from "../../public/logo.png";
import styles from "./page.module.css";

const ThemeToggle = dynamic(() => import("./components/ThemeToggle"), {});
const CopyButton = dynamic(() => import("./components/CopyButton"), {});

const windowsInstall = `git clone https://github.com/thirschel/Hangar.git
cd Hangar
build.bat`;

const homebrewInstall = "brew install claude-squad";
const shellInstall = `# Download, verify, then install (replace VERSION with a release tag):
# See https://github.com/thirschel/Hangar/releases for all versions.
VERSION="v1.0.0"
curl -fsSL \\
  "https://github.com/thirschel/Hangar/releases/download/\${VERSION}/install.sh" \\
  -o install-hangar.sh
sha256sum install-hangar.sh   # compare against the checksum on the release page
bash install-hangar.sh`;

const features = [
  {
    title: "Runs natively on Windows",
    body: "No WSL, no tmux. A background session host owns a real Windows console (ConPTY) per agent; the cs TUI talks to it over a named pipe and renders via a VT emulator. Sessions survive TUI restarts.",
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
  {
    title: "Attach / detach",
    body: "Press Enter to attach to a session, Ctrl+q to detach back to the dashboard; Ctrl+c passes through to the agent.",
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
            The native-Windows-first manager for your AI coding agents.
          </p>
        </section>

        <div className={styles.demoVideo}>
          <video
            controls
            autoPlay
            muted
            loop
            playsInline
            className={styles.video}
            src="https://github.com/user-attachments/assets/aef18253-e58f-4525-9032-f5a3d66c975a"
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

        <section className={styles.installation} id="install" aria-labelledby="install-title">
          <div className={styles.sectionIntro}>
            <p className={styles.sectionKicker}>Start</p>
            <h2 id="install-title">Install Hangar</h2>
          </div>

          <div className={styles.installGrid}>
            <article className={styles.installCard}>
              <div>
                <p className={styles.platform}>Native Windows (primary)</p>
                <h3>Build this fork locally</h3>
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
            </article>

            <article className={styles.installCard}>
              <div>
                <p className={styles.platform}>Unix/macOS</p>
                <h3>Download, verify, then install</h3>
                <p>
                  Download a pinned release, verify its SHA256, then run the install script.
                  See the <a href="https://github.com/thirschel/Hangar/releases" target="_blank" rel="noopener noreferrer">releases page</a> for available versions and published checksums.
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
