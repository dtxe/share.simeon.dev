import { Redirect, Route, Switch } from 'wouter'
import BillWorkspace from './screens/BillWorkspace'
import SettleScreen from './screens/Settle'
import HistoryScreen from './screens/History'
import SharedView from './screens/SharedView'

export default function App() {
  return (
    <Switch>
      <Route path="/" component={BillWorkspace} />
      <Route path="/bill/:id" component={BillWorkspace} />
      <Route path="/bill/:id/settle" component={SettleScreen} />
      <Route path="/history" component={HistoryScreen} />
      <Route path="/s/:token" component={SharedView} />

      <Route path="/bill/:id/people">{(p) => <Redirect to={`/bill/${p.id}`} />}</Route>
      <Route path="/bill/:id/items">{(p) => <Redirect to={`/bill/${p.id}`} />}</Route>
      <Route path="/bill/:id/assign">{(p) => <Redirect to={`/bill/${p.id}`} />}</Route>
      <Route path="/bill/:id/results">{(p) => <Redirect to={`/bill/${p.id}/settle`} />}</Route>

      <Route>
        <div className="p-6 text-center text-sm text-neutral-500">Page not found.</div>
      </Route>
    </Switch>
  )
}
