import { DatabaseIcon, FolderIcon, GitBranchIcon } from "lucide-react"
import type { CSSProperties } from "react"
import { Stage } from "./stage"

const CX = 220
const CY = 160
const R = 32
const MOON = 16
// How flat the circles are drawn, so they read as orbits seen from above.
const TILT = 0.66
// The orbits are spaced so two moons can never touch, whatever their speeds:
// the closest two orbits get is 55 * TILT, a few units more than one moon across.
const moons = [
  { r: 83, seconds: 18, angle: 20, icon: GitBranchIcon },
  { r: 138, seconds: 27, angle: 200, icon: GitBranchIcon },
  { r: 193, seconds: 38, angle: 110, icon: FolderIcon },
]
// A whole number of dots, so the dotted line has no seam where it closes.
const dots = (r: number) => Math.round((Math.PI * (3 * (1 + TILT) - Math.sqrt((3 + TILT) * (1 + 3 * TILT))) * r) / 8) * 8

// The hero graph: main sits in the middle, and its branches and files circle
// it. The icons say which is which, so nothing has to be read while it moves.
// The loop itself lives in landing.css (the orbit-* classes). Each moon is spun
// inside a flattened group, then unspun and unflattened so it stays upright.
export function Orbit() {
  return (
    <Stage label="The main database sits in the middle. Two branches and a file bucket circle it.">
      <svg viewBox="0 0 440 320" className="notranslate mx-auto block w-full max-w-[520px]" aria-hidden="true">
        {moons.map((moon) => (
          <ellipse
            data-orbit
            key={moon.r}
            cx={CX}
            cy={CY}
            rx={moon.r}
            ry={moon.r * TILT}
            pathLength={dots(moon.r)}
            className="fill-none stroke-faint"
            strokeDasharray="1 7"
            strokeLinecap="round"
          />
        ))}
        {[0, 1.2].map((delay) => (
          <circle
            key={delay}
            cx={CX}
            cy={CY}
            r={R}
            className="ring-pulse origin-center fill-none stroke-(--mark) opacity-0 [transform-box:fill-box]"
            style={{ "--at": `-${delay}s` } as CSSProperties}
          />
        ))}
        <circle cx={CX} cy={CY} r={R} className="fill-(--mark)" />
        <DatabaseIcon x={CX - 11} y={CY - 11} size={22} className="text-background" />

        {moons.map((moon) => {
          const turn = { "--turn": `${moon.seconds}s` } as CSSProperties
          return (
            <g key={moon.r} transform={`translate(${CX} ${CY}) scale(1 ${TILT}) rotate(${moon.angle})`}>
              <g className="orbit-spin" style={turn}>
                <g transform={`translate(${moon.r} 0)`}>
                  <g className="orbit-unspin" style={turn}>
                    <g transform={`rotate(${-moon.angle}) scale(1 ${1 / TILT})`}>
                      <circle data-moon r={MOON} className="fill-background stroke-muted-foreground" />
                      <moon.icon x={-MOON / 2} y={-MOON / 2} size={MOON} className="text-foreground" />
                    </g>
                  </g>
                </g>
              </g>
            </g>
          )
        })}
      </svg>
    </Stage>
  )
}
