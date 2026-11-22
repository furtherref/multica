import { execFileSync } from "node:child_process";
import { join } from "node:path";

export function verifyMacRelease(context, execute = execFileSync, env = process.env) {
  if (context.electronPlatformName !== "darwin") return;
  const policy = context.packager.info.options.publish;
  if (!policy || policy === "never") return;
  // The fork's release workflow turns signing off explicitly and ships
  // unsigned macOS builds; an unsigned app can never pass these checks.
  if (env.CSC_IDENTITY_AUTO_DISCOVERY === "false") return;
  // afterSign runs after built-in notarization, before ZIP/DMG creation and
  // publication. Checking in the skill after upload would race CI promotion.
  const app = join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`);
  const options = { stdio: "inherit", timeout: 60_000 };
  execute("spctl", ["--assess", "--type", "execute", "--verbose", app], options);
  execute("xcrun", ["stapler", "validate", app], options);
}

export default verifyMacRelease;
