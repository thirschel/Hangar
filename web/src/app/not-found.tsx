import Image from "next/image";
import Link from "next/link";
import logo from "../../public/logo.png";
import styles from "./page.module.css";

export default function NotFound() {
  return (
    <main className={styles.main}>
      <section className={styles.hero} aria-labelledby="not-found-title">
        <span className={`${styles.logoChip} ${styles.heroLogoChip}`}>
          <Image src={logo} alt="Hangar logo" className={styles.heroLogo} priority />
        </span>
        <p className={styles.eyebrow}>Hangar</p>
        <h1 id="not-found-title">Page not found</h1>
        <p className={styles.positioning}>
          This copilot bay is empty. Head back to the Hangar homepage.
        </p>
        <Link className={styles.headerButton} href="/">
          Back home
        </Link>
      </section>
    </main>
  );
}
