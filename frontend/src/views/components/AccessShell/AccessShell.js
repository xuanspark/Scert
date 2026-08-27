export const accessShellProps = {
  variant: {
    type: String,
    default: "default",
    validator: (value) => ["default", "setup", "compact"].includes(value),
  },
  ariaLabel: {
    type: String,
    default: "Scert 访问页面",
  },
};
