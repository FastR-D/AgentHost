import { memo, type AnchorHTMLAttributes } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import clsx from "clsx";

/** Links to server-local paths (e.g. "/repo/file.go:12") cannot be opened from
 *  the browser, so show them as code; real URLs open in a new tab. */
export function MdLink({ href, children, ...rest }: AnchorHTMLAttributes<HTMLAnchorElement>) {
  if (!href || href.startsWith("/") || href.startsWith("file:") || href.startsWith(".")) {
    return (
      <code className="cursor-help" title={href ? href.replace(/^file:\/\//, "") : undefined}>
        {children}
      </code>
    );
  }
  return (
    <a href={href} target="_blank" rel="noreferrer noopener" {...rest}>
      {children}
    </a>
  );
}

export const mdComponents = { a: MdLink };

// CommonMark ignores "**问题：**后面" (a closing ** after full-width
// punctuation, followed by a letter) and "前**“引号”**" (the mirror case),
// which Chinese text does all the time. Move that punctuation outside.
const CLOSE_PUNCT = "：:，,。；;！!？?、）)」』】》”’";
const OPEN_PUNCT = "（(「『【《“‘";
const reClose = new RegExp(`\\*\\*([^*\\n]*?[^*\\s])([${CLOSE_PUNCT}]+)\\*\\*(?=[^\\s*${CLOSE_PUNCT}])`, "g");
const reOpen = new RegExp(`(\\S)\\*\\*([${OPEN_PUNCT}]+)([^*\\n]+?)\\*\\*`, "g");

export function fixCjkEmphasis(text: string): string {
  if (!text.includes("**")) return text;
  return text.replace(reClose, "**$1**$2").replace(reOpen, "$1$2**$3**");
}

export const Markdown = memo(function Markdown({ text, className }: { text: string; className?: string }) {
  return (
    <div className={clsx("md", className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>
        {fixCjkEmphasis(text)}
      </ReactMarkdown>
    </div>
  );
});
