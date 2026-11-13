import { Picker } from "../picker"
import { AgentChat } from "./agent-chat"
import { StageDemo } from "./stage-demo"
import { Terminal } from "./terminal"

export default function HeroPrototype() {
  return (
    <Picker
      replay
      variants={[
        { name: "Product stage", component: StageDemo },
        { name: "Terminal", component: Terminal },
        { name: "Agent chat", component: AgentChat },
      ]}
    />
  )
}
