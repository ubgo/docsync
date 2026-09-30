'use strict';

// Two entry points: `remark` for docusaurus.config.js `remarkPlugins`, and
// the default export as a Docusaurus plugin providing freshness data.
const plugin = require('./plugin');
const remark = require('./remark');

module.exports = plugin;
module.exports.remark = remark;
module.exports.plugin = plugin;
