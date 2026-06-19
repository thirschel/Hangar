import nextVitals from "eslint-config-next/core-web-vitals";
import nextTypescript from "eslint-config-next/typescript";

function wrapVisitor(visitor, setCurrentNode) {
  return Object.fromEntries(
    Object.entries(visitor).map(([selector, handler]) => {
      if (typeof handler === "function") {
        return [selector, function (node, ...args) {
          setCurrentNode(node);
          return handler.call(this, node, ...args);
        }];
      }

      if (handler && typeof handler === "object") {
        return [selector, {
          ...handler,
          enter: typeof handler.enter === "function"
            ? function (node, ...args) {
                setCurrentNode(node);
                return handler.enter.call(this, node, ...args);
              }
            : handler.enter,
          exit: typeof handler.exit === "function"
            ? function (node, ...args) {
                setCurrentNode(node);
                return handler.exit.call(this, node, ...args);
              }
            : handler.exit,
        }];
      }

      return [selector, handler];
    }),
  );
}

function fixupRule(rule) {
  if (!rule || typeof rule.create !== "function") {
    return rule;
  }

  return {
    ...rule,
    create(context) {
      let currentNode;
      const sourceCode = context.sourceCode ?? context.getSourceCode?.();
      const compatContext = new Proxy(context, {
        get(target, prop, receiver) {
          if (prop === "getSourceCode") {
            return () => sourceCode;
          }
          if (prop === "getFilename") {
            return () => target.filename;
          }
          if (prop === "getPhysicalFilename") {
            return () => target.physicalFilename ?? target.filename;
          }
          if (prop === "getScope") {
            return () => sourceCode.getScope(currentNode);
          }
          if (prop === "getAncestors") {
            return () => sourceCode.getAncestors(currentNode);
          }
          if (prop === "markVariableAsUsed") {
            return (name) => sourceCode.markVariableAsUsed(name, currentNode);
          }
          if (prop === "getDeclaredVariables") {
            return (node) => sourceCode.getDeclaredVariables(node);
          }

          return Reflect.get(target, prop, receiver);
        },
      });

      return wrapVisitor(rule.create(compatContext), (node) => {
        currentNode = node;
      });
    },
  };
}

const fixedPlugins = new WeakMap();

function fixupPlugin(plugin) {
  if (!plugin?.rules) {
    return plugin;
  }

  if (fixedPlugins.has(plugin)) {
    return fixedPlugins.get(plugin);
  }

  const fixedPlugin = {
    meta: plugin.meta,
    processors: plugin.processors,
    rules: Object.fromEntries(
      Object.entries(plugin.rules).map(([name, rule]) => [name, fixupRule(rule)]),
    ),
  };

  fixedPlugins.set(plugin, fixedPlugin);
  return fixedPlugin;
}

function fixupConfig(configs) {
  return configs.map((config) => {
    if (!config.plugins) {
      return config;
    }

    return {
      ...config,
      plugins: Object.fromEntries(
        Object.entries(config.plugins).map(([name, plugin]) => [name, fixupPlugin(plugin)]),
      ),
    };
  });
}

const eslintConfig = fixupConfig([
  ...nextVitals,
  ...nextTypescript,
  {
    rules: {
      "react-hooks/set-state-in-effect": "off",
    },
  },
]);

export default eslintConfig;
