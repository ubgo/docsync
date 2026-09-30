'use strict';

// docusaurusPluginDocsync exposes `ds status --json` to the site as global
// data, so a theme component can paint green, amber, and red dots beside
// cited sentences (spec §24 "docs site"). The remark plugin does the
// rendering; this one only loads freshness.
//
// Options: command, args, cwd, exec as in remark.js.

const { execFileSync } = require('node:child_process');

const PLUGIN_NAME = 'docusaurus-plugin-docsync';
const DEFAULT_COMMAND = 'ds';
const STATUS_ARGS = ['status', '--json'];

function defaultExec(command, args, options) {
  return execFileSync(command, args, { ...options, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
}

function docusaurusPluginDocsync(context, options = {}) {
  const { command = DEFAULT_COMMAND, args = [], cwd = process.cwd(), exec = defaultExec } = options;
  return {
    name: PLUGIN_NAME,
    async loadContent() {
      const raw = exec(command, [...args, ...STATUS_ARGS], { cwd });
      const status = JSON.parse(raw);
      if (!status || !Array.isArray(status.refs)) {
        throw new Error(`docsync: ${command} ${STATUS_ARGS.join(' ')} returned no refs`);
      }
      return status;
    },
    async contentLoaded({ content, actions }) {
      // Indexed by doc so a page looks up its own lines in O(1).
      const byDoc = {};
      for (const ref of content.refs) {
        (byDoc[ref.doc] = byDoc[ref.doc] || []).push(ref);
      }
      actions.setGlobalData({ commit: content.commit, refs: content.refs, byDoc });
    },
  };
}

module.exports = docusaurusPluginDocsync;
module.exports.PLUGIN_NAME = PLUGIN_NAME;
